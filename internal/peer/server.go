package peer

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lmangani/llmirror/internal/auth"
	"github.com/lmangani/llmirror/internal/cache"
	"github.com/lmangani/llmirror/internal/netacl"
)

var blobHashRe = regexp.MustCompile(`^[a-fA-F0-9]{40,64}$`)

// Server exposes the local HF cache over HTTP for peer transfers.
type Server struct {
	HubDir string
	Addr   string
	Token  string
	Group  string
	ACL    *netacl.ACL
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/info", s.handleInfo)
	mux.HandleFunc("/v1/models", s.handleListModels)
	mux.HandleFunc("/v1/models/", s.handleModelRoutes)
	mux.HandleFunc("/v1/blobs/", s.handleBlob)

	// Authenticated API surface (token + group).
	var api http.Handler = mux
	api = auth.GroupMiddleware(s.Group, api)
	api = auth.Middleware(s.Token, api)

	root := http.NewServeMux()
	root.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Llmirror-Group", s.Group)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	root.Handle("/", api)

	var h http.Handler = root
	if s.ACL != nil {
		h = s.ACL.Middleware(h)
	}
	return h
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.Addr, s.Handler())
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"service": "llmirror",
		"group":   s.Group,
		"auth":    s.Token != "",
	})
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := cache.ScanModels(s.HubDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models)
}

func (s *Server) handleModelRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	rest = strings.Trim(rest, "/")

	var action string
	switch {
	case strings.HasSuffix(rest, "/has"):
		action = "has"
		rest = strings.TrimSuffix(rest, "/has")
	case strings.HasSuffix(rest, "/manifest"):
		action = "manifest"
		rest = strings.TrimSuffix(rest, "/manifest")
	default:
		http.NotFound(w, r)
		return
	}

	repoID, err := url.PathUnescape(rest)
	if err != nil {
		http.Error(w, "invalid repo id", http.StatusBadRequest)
		return
	}
	repoID = strings.ReplaceAll(repoID, "%2F", "/")

	revision := r.URL.Query().Get("revision")
	if revision == "" {
		revision = "main"
	}
	repoType := cache.ParseRepoType(r.URL.Query().Get("repo_type"))

	switch action {
	case "has":
		s.handleHas(w, repoID, revision, repoType)
	case "manifest":
		s.handleManifest(w, repoID, revision, repoType)
	}
}

func (s *Server) handleHas(w http.ResponseWriter, repoID, revision string, repoType cache.RepoType) {
	ok, err := cache.HasRevision(s.HubDir, repoID, revision, repoType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{
		"repo_id":   repoID,
		"repo_type": repoType.String(),
		"revision":  revision,
		"present":   ok,
	}
	if ok {
		repoDir := filepath.Join(s.HubDir, cache.RepoFolderName(repoID, repoType))
		if hash, err := cache.ResolveRevision(repoDir, revision); err == nil {
			resp["revision_hash"] = hash
		}
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleManifest(w http.ResponseWriter, repoID, revision string, repoType cache.RepoType) {
	repoDir := filepath.Join(s.HubDir, cache.RepoFolderName(repoID, repoType))
	hash, err := cache.ResolveRevision(repoDir, revision)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	files, err := cache.SnapshotManifest(repoDir, hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"repo_id":       repoID,
		"repo_type":     repoType.String(),
		"revision":      revision,
		"revision_hash": hash,
		"files":         files,
	})
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/v1/blobs/")
	if !blobHashRe.MatchString(hash) {
		http.NotFound(w, r)
		return
	}

	path, err := findBlob(s.HubDir, hash)
	if err != nil {
		http.Error(w, "blob not found", http.StatusNotFound)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	info, _ := f.Stat()
	var mod time.Time
	if info != nil {
		mod = info.ModTime()
	}
	http.ServeContent(w, r, hash, mod, f)
}

func findBlob(hubDir, hash string) (string, error) {
	entries, err := os.ReadDir(hubDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(hubDir, entry.Name(), "blobs", hash)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("blob %s not found", hash)
}

// LocalListenAddr picks a free TCP port on all interfaces.
func LocalListenAddr() (string, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr, nil
}
