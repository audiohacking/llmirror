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
	"github.com/lmangani/llmirror/internal/netacl"
	"github.com/lmangani/llmirror/internal/peer"
)

const defaultUpstream = "https://huggingface.co"

// Config for the HF_ENDPOINT reverse proxy.
type Config struct {
	HubDir      string
	Addr        string
	Upstream    string
	PeersFile   string
	PeerTimeout time.Duration
	SkipPeers   bool
	Token       string
	Group       string
	ACL         *netacl.ACL
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
		peers: peer.NewClient(cfg.Token, cfg.Group),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	var h http.Handler = mux
	// CDN proxy is for local Python libs — still apply ACL (defaults to loopback+private).
	if s.cfg.ACL != nil {
		h = s.cfg.ACL.Middleware(h)
	}
	return h
}

func (s *Server) ListenAndServe() error {
	log.Printf("llmirror cdn-proxy: listening on %s", s.cfg.Addr)
	log.Printf("llmirror cdn-proxy: set HF_ENDPOINT=http://127.0.0.1%s", displayPort(s.cfg.Addr))
	log.Printf("llmirror cdn-proxy: upstream %s, hub cache %s", s.cfg.Upstream, s.cfg.HubDir)
	return http.ListenAndServe(s.cfg.Addr, s.Handler())
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

	repoType := cache.ParseRepoType(req.RepoType)

	if abs, blobHash, size, err := cache.ResolveFilePath(s.cfg.HubDir, req.RepoID, req.Revision, req.Filename, repoType); err == nil {
		log.Printf("cdn-proxy: local %s@%s/%s", req.RepoID, req.Revision, req.Filename)
		s.serveFile(w, r, abs, blobHash, size)
		return
	}

	if !s.cfg.SkipPeers {
		if s.tryPeers(w, r, req, repoType) {
			return
		}
	}

	log.Printf("cdn-proxy: upstream %s@%s/%s", req.RepoID, req.Revision, req.Filename)
	s.proxyUpstream(w, r)
}

// ResolveReq is a parsed Hub file download URL.
type ResolveReq struct {
	RepoID   string
	Revision string
	Filename string
	RepoType string
}

// ParseResolvePath matches HF Hub download URL shapes used with HF_ENDPOINT.
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

	// Local/peer cache covers models, datasets, and spaces.
	return ResolveReq{
		RepoID:   repoID,
		Revision: revision,
		Filename: filename,
		RepoType: repoType,
	}, true
}

func (s *Server) tryPeers(w http.ResponseWriter, r *http.Request, req ResolveReq, repoType cache.RepoType) bool {
	peers, err := peer.DiscoverPeers(s.cfg.PeersFile, s.cfg.PeerTimeout, s.cfg.Group)
	if err != nil || len(peers) == 0 {
		return false
	}
	peerURL, err := peer.FindPeerWithModel(peers, req.RepoID, req.Revision, repoType, s.cfg.Token, s.cfg.Group)
	if err != nil {
		return false
	}
	blobHash, size, ok, err := s.peers.PeerHasFile(peerURL, req.RepoID, req.Revision, req.Filename, repoType)
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

	// Prefer client-provided Hub auth; otherwise inject HF_TOKEN from the proxy host.
	if outReq.Header.Get("Authorization") == "" {
		if tok := hfTokenFromEnv(); tok != "" {
			outReq.Header.Set("Authorization", "Bearer "+tok)
		}
	}

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

func hfTokenFromEnv() string {
	if v := os.Getenv("HF_TOKEN"); v != "" {
		return v
	}
	return os.Getenv("HUGGING_FACE_HUB_TOKEN")
}