package peer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lmangani/llmirror/internal/cache"
)

func TestHasRevisionEndpoint(t *testing.T) {
	hub := t.TempDir()
	repoID := "org/model"
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	repo := filepath.Join(hub, cache.RepoFolderName(repoID))
	_ = os.MkdirAll(filepath.Join(repo, "snapshots", hash), 0o755)
	_ = os.MkdirAll(filepath.Join(repo, "refs"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "refs", "main"), []byte(hash), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "snapshots", hash, "a.bin"), []byte("data"), 0o644)

	srv := &Server{HubDir: hub}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := NewClient()
	has, err := client.HasRevision(ts.URL, repoID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !has.Present || has.RevisionHash != hash {
		t.Fatalf("got %+v", has)
	}

	has, err = client.HasRevision(ts.URL, repoID, "other")
	if err != nil {
		t.Fatal(err)
	}
	if has.Present {
		t.Fatal("expected missing revision")
	}

	peerURL, err := FindPeerWithModel([]string{ts.URL}, repoID, "main")
	if err != nil || peerURL != ts.URL {
		t.Fatalf("FindPeerWithModel: %v %s", err, peerURL)
	}
	_, err = FindPeerWithModel([]string{ts.URL}, repoID, "nope")
	if err == nil {
		t.Fatal("expected error for missing revision")
	}
}

func TestBlobRangeResume(t *testing.T) {
	hub := t.TempDir()
	hash := "cccccccccccccccccccccccccccccccccccccccc"
	repo := filepath.Join(hub, "models--org--m")
	blobDir := filepath.Join(repo, "blobs")
	_ = os.MkdirAll(blobDir, 0o755)
	payload := []byte("0123456789abcdef")
	_ = os.WriteFile(filepath.Join(blobDir, hash), payload, 0o644)

	srv := &Server{HubDir: hub}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := NewClient()
	var buf []byte
	w := &appendWriter{b: &buf}
	if err := client.FetchBlob(ts.URL, hash, 0, w); err != nil {
		t.Fatal(err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("full fetch: %q", buf)
	}

	buf = nil
	if err := client.FetchBlob(ts.URL, hash, 8, w); err != nil {
		t.Fatal(err)
	}
	if string(buf) != string(payload[8:]) {
		t.Fatalf("range fetch: %q", buf)
	}
}

type appendWriter struct{ b *[]byte }

func (a *appendWriter) Write(p []byte) (int, error) {
	*a.b = append(*a.b, p...)
	return len(p), nil
}

func TestListModelsIncludesRefs(t *testing.T) {
	hub := t.TempDir()
	hash := "dddddddddddddddddddddddddddddddddddddddd"
	repo := filepath.Join(hub, cache.RepoFolderName("org/m"))
	_ = os.MkdirAll(filepath.Join(repo, "snapshots", hash), 0o755)
	_ = os.MkdirAll(filepath.Join(repo, "refs"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "refs", "main"), []byte(hash), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "snapshots", hash, "f"), []byte("x"), 0o644)

	srv := &Server{HubDir: hub}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var models []cache.Model
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Refs["main"] != hash {
		t.Fatalf("got %+v", models)
	}
}
