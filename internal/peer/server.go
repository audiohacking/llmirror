package peer

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lmangani/llmirror/internal/cache"
)

// Server exposes the local HF cache over HTTP for peer transfers.
type Server struct {
	HubDir string
	Addr   string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", s.handleListModels)
	mux.HandleFunc("/v1/models/", s.handleModelRoutes)
	mux.HandleFunc("/v1/blobs/", s.handleBlob)
	return mux
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.Addr, s.Handler())
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
	// Accept both org/model and org%2Fmodel forms.
	repoID = strings.ReplaceAll(repoID, "%2F", "/")

	revision := r.URL.Query().Get("revision")
	if revision == "" {
		revision = "main"
	}

	switch action {
	case "has":
		s.handleHas(w, repoID, revision)
	case "manifest":
		s.handleManifest(w, repoID, revision)
	}
}

func (s *Server) handleHas(w http.ResponseWriter, repoID, revision string) {
	ok, err := cache.HasRevision(s.HubDir, repoID, revision)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{
		"repo_id":  repoID,
		"revision": revision,
		"present":  ok,
	}
	if ok {
		repoDir := filepath.Join(s.HubDir, cache.RepoFolderName(repoID))
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

func (s *Server) handleManifest(w http.ResponseWriter, repoID, revision string) {
	repoDir := filepath.Join(s.HubDir, cache.RepoFolderName(repoID))
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
		"revision":      revision,
		"revision_hash": hash,
		"files":         files,
	})
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/v1/blobs/")
	if hash == "" || strings.Contains(hash, "/") || strings.Contains(hash, "..") {
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
