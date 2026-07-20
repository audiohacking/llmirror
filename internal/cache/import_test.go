package cache

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBlobResumable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob123")
	payload := []byte("hello-resumable-world-0123456789")

	// First attempt: write half, then fail.
	half := len(payload) / 2
	err := writeBlobResumable(path, int64(len(payload)), "blob123", func(hash string, offset int64, w io.Writer) error {
		if offset != 0 {
			t.Fatalf("expected offset 0, got %d", offset)
		}
		if _, err := w.Write(payload[:half]); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	})
	if err == nil {
		t.Fatal("expected error on first attempt")
	}
	partial := path + ".partial"
	info, err := os.Stat(partial)
	if err != nil {
		t.Fatal("expected .partial file")
	}
	if info.Size() != int64(half) {
		t.Fatalf("partial size %d want %d", info.Size(), half)
	}

	// Second attempt: resume from offset.
	err = writeBlobResumable(path, int64(len(payload)), "blob123", func(hash string, offset int64, w io.Writer) error {
		if offset != int64(half) {
			t.Fatalf("expected offset %d, got %d", half, offset)
		}
		_, err := w.Write(payload[half:])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("partial should be renamed away")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q want %q", got, payload)
	}
}

func TestResolveRevisionNamedRef(t *testing.T) {
	hub := t.TempDir()
	repo := filepath.Join(hub, RepoFolderName("org/model"))
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.MkdirAll(filepath.Join(repo, "snapshots", hash), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "refs", "main"), []byte(hash+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a file so HasRevision is true.
	if err := os.WriteFile(filepath.Join(repo, "snapshots", hash, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveRevision(repo, "main")
	if err != nil || got != hash {
		t.Fatalf("ResolveRevision: %v %q", err, got)
	}
	ok, err := HasRevision(hub, "org/model", "main")
	if err != nil || !ok {
		t.Fatalf("HasRevision: %v %v", err, ok)
	}
	ok, err = HasRevision(hub, "org/model", "missing")
	if err != nil || ok {
		t.Fatalf("HasRevision missing: %v %v", err, ok)
	}
}
