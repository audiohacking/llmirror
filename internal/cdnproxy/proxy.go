package cdnproxy

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/lmangani/llmirror/internal/cache"
	"github.com/lmangani/llmirror/internal/peer"
)

const defaultUpstream = "https://huggingface.co"

// Config for the HF_ENDPOINT reverse proxy.
type Config struct {
	HubDir      string
	Addr        string
	Upstream    string // e.g. https://huggingface.co
	PeersFile   string
	PeerTimeout time.Duration
	SkipPeers   bool
}

// Server intercepts Hub resolve/raw downloads: local → peers → upstream.
type Server struct {
	cfg    Config
	client *http.Client
	peers  *peer.Client
}

func New(cfg Config) *Server {
	if cfg.Upstream == "" {
		cfg.Upstream = defaultUpstream
	}
	if cfg.PeerTimeout == 0 {
		cfg.PeerTimeout = 3 * time.Second
	}
	return &Server{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		peers: peer.NewClient(),
	}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	log.Printf("llmirror cdn-proxy: listening on %s", s.cfg.Addr)
	log.Printf("llmirror cdn-proxy: set HF_ENDPOINT=http://127.0.0.1%s", displayPort(s.cfg.Addr))
	log.Printf("llmirror cdn-proxy: upstream %s, hub cache %s", s.cfg.Upstream, s.cfg.HubDir)
	return http.ListenAndServe(s.cfg.Addr, mux)
}

func displayPort(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	if _, port, ok := strings.Cut(addr, ":"); ok && port != "" {
		return ":" + port
	}
	return addr
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.proxyUpstream(w, r)
		return
	}

	req, ok := ParseResolvePath(r.URL.Path)
	if !ok {
		s.proxyUpstream(w, r)
		return
	}

	// 1) Local HF cache
	if abs, blobHash, size, err := cache.ResolveFilePath(s.cfg.HubDir, req.RepoID, req.Revision, req.Filename); err == nil {
		log.Printf("cdn-proxy: local %s@%s/%s", req.RepoID, req.Revision, req.Filename)
		s.serveFile(w, r, abs, blobHash, size)
		return
	}

	// 2) Fleet peers
	if !s.cfg.SkipPeers {
		if s.tryPeers(w, r, req) {
			return
		}
	}

	// 3) Upstream Hugging Face (follows CDN redirects)
	log.Printf("cdn-proxy: upstream %s@%s/%s", req.RepoID, req.Revision, req.Filename)
	s.proxyUpstream(w, r)
}

// ResolveReq is a parsed Hub file download URL.
type ResolveReq struct {
	RepoID   string
	Revision string
	Filename string
	RepoType string // model, dataset, space
}

// ParseResolvePath matches HF Hub download URL shapes used with HF_ENDPOINT:
//
//	/{repo_id}/resolve/{revision}/{filename}
//	/{repo_id}/raw/{revision}/{filename}
//	/models/{repo_id}/resolve/{revision}/{filename}
//	/datasets/{repo_id}/resolve/{revision}/{filename}
func ParseResolvePath(urlPath string) (ResolveReq, bool) {
	urlPath = path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 4 {
		return ResolveReq{}, false
	}

	repoType := "model"
	switch parts[0] {
	case "datasets":
		repoType = "dataset"
		parts = parts[1:]
	case "spaces":
		repoType = "space"
		parts = parts[1:]
	case "models":
		repoType = "model"
		parts = parts[1:]
	}

	kindIdx := -1
	for i, p := range parts {
		if p == "resolve" || p == "raw" {
			kindIdx = i
			break
		}
	}
	// Need: [repo...] resolve|raw revision filename...
	if kindIdx < 1 || kindIdx+2 >= len(parts) {
		return ResolveReq{}, false
	}

	repoID := strings.Join(parts[:kindIdx], "/")
	revision, err := url.PathUnescape(parts[kindIdx+1])
	if err != nil {
		revision = parts[kindIdx+1]
	}
	filename := strings.Join(parts[kindIdx+2:], "/")
	filename, _ = url.PathUnescape(filename)

	// Local/peer cache currently only covers model hub folders (models--...).
	if repoType != "model" {
		return ResolveReq{}, false
	}

	return ResolveReq{
		RepoID:   repoID,
		Revision: revision,
		Filename: filename,
		RepoType: repoType,
	}, true
}

func (s *Server) tryPeers(w http.ResponseWriter, r *http.Request, req ResolveReq) bool {
	peers, err := peer.DiscoverPeers(s.cfg.PeersFile, s.cfg.PeerTimeout)
	if err != nil || len(peers) == 0 {
		return false
	}
	peerURL, err := peer.FindPeerWithModel(peers, req.RepoID, req.Revision)
	if err != nil {
		return false
	}
	blobHash, size, ok, err := s.peers.PeerHasFile(peerURL, req.RepoID, req.Revision, req.Filename)
	if err != nil || !ok {
		return false
	}
	log.Printf("cdn-proxy: peer %s %s@%s/%s", peerURL, req.RepoID, req.Revision, req.Filename)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", `"`+blobHash+`"`)
	w.Header().Set("X-Llmirror-Source", "peer")
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return true
	}
	// Once we start writing the body, the response is committed — do not fall back.
	if err := s.peers.FetchBlob(peerURL, blobHash, 0, w); err != nil {
		log.Printf("cdn-proxy: peer fetch error (response may be partial): %v", err)
	}
	return true
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, absPath, blobHash string, size int64) {
	f, err := os.Open(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("ETag", `"`+blobHash+`"`)
	w.Header().Set("X-Llmirror-Source", "local")
	info, _ := f.Stat()
	var mod time.Time
	if info != nil {
		mod = info.ModTime()
	}
	_ = size
	http.ServeContent(w, r, path.Base(absPath), mod, f)
}

func (s *Server) proxyUpstream(w http.ResponseWriter, r *http.Request) {
	upstream, err := url.Parse(s.cfg.Upstream)
	if err != nil {
		http.Error(w, "bad upstream", http.StatusInternalServerError)
		return
	}

	outURL := *r.URL
	outURL.Scheme = upstream.Scheme
	outURL.Host = upstream.Host

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(outReq.Header, r.Header)
	outReq.Header.Del("Accept-Encoding")
	outReq.Host = upstream.Host

	resp, err := s.client.Do(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("X-Llmirror-Source", "upstream")
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		switch strings.ToLower(k) {
		case "connection", "proxy-connection", "keep-alive", "transfer-encoding", "upgrade":
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
