package peer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lmangani/llmirror/internal/cache"
)

const defaultTimeout = 30 * time.Minute

// Client talks to llmirror HTTP servers on peer hosts.
type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: defaultTimeout},
	}
}

type PeerModel struct {
	RepoID    string            `json:"repo_id"`
	Revisions []string          `json:"revisions"`
	Refs      map[string]string `json:"refs"`
}

func (c *Client) ListModels(baseURL string) ([]PeerModel, error) {
	resp, err := c.HTTP.Get(strings.TrimRight(baseURL, "/") + "/v1/models")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer %s: %s", baseURL, resp.Status)
	}
	var models []PeerModel
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, err
	}
	return models, nil
}

type HasResponse struct {
	RepoID       string `json:"repo_id"`
	Revision     string `json:"revision"`
	RevisionHash string `json:"revision_hash"`
	Present      bool   `json:"present"`
}

// HasRevision asks a peer whether it has an exact revision (named ref or commit).
func (c *Client) HasRevision(baseURL, repoID, revision string) (*HasResponse, error) {
	u := fmt.Sprintf("%s/v1/models/%s/has?revision=%s",
		strings.TrimRight(baseURL, "/"),
		escapeRepo(repoID),
		url.QueryEscape(revision),
	)
	resp, err := c.HTTP.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &HasResponse{RepoID: repoID, Revision: revision, Present: false}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer has: %s", resp.Status)
	}
	var h HasResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, err
	}
	return &h, nil
}

type ManifestResponse struct {
	RepoID       string                `json:"repo_id"`
	Revision     string                `json:"revision"`
	RevisionHash string                `json:"revision_hash"`
	Files        []cache.ManifestEntry `json:"files"`
}

func (c *Client) FetchManifest(baseURL, repoID, revision string) (*ManifestResponse, error) {
	u := fmt.Sprintf("%s/v1/models/%s/manifest?revision=%s",
		strings.TrimRight(baseURL, "/"),
		escapeRepo(repoID),
		url.QueryEscape(revision),
	)
	resp, err := c.HTTP.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer manifest: %s", resp.Status)
	}
	var m ManifestResponse
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// FetchBlob downloads a blob, optionally resuming from offset via HTTP Range.
func (c *Client) FetchBlob(baseURL, blobHash string, offset int64, w io.Writer) error {
	u := fmt.Sprintf("%s/v1/blobs/%s", strings.TrimRight(baseURL, "/"), blobHash)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if offset > 0 {
			// Server ignored Range — caller must restart from zero.
			return fmt.Errorf("peer blob %s: expected 206 for range offset %d, got 200", blobHash, offset)
		}
	case http.StatusPartialContent:
		// resume OK
	default:
		return fmt.Errorf("peer blob %s: %s", blobHash, resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func escapeRepo(repoID string) string {
	return strings.ReplaceAll(repoID, "/", "%2F")
}

// CopyFromPeer pulls a model revision from a peer into the local HF cache.
func CopyFromPeer(hubDir, baseURL, repoID, revision string) error {
	client := NewClient()
	manifest, err := client.FetchManifest(baseURL, repoID, revision)
	if err != nil {
		return err
	}
	fetch := func(blobHash string, offset int64, w io.Writer) error {
		return client.FetchBlob(baseURL, blobHash, offset, w)
	}
	return cache.ImportSnapshot(hubDir, repoID, revision, manifest.RevisionHash, manifest.Files, fetch)
}

// FindPeerWithModel returns the first peer that has the exact requested revision.
func FindPeerWithModel(peers []string, repoID, revision string) (string, error) {
	client := NewClient()
	var lastErr error
	for _, peerURL := range peers {
		has, err := client.HasRevision(peerURL, repoID, revision)
		if err != nil {
			lastErr = err
			continue
		}
		if has != nil && has.Present {
			return peerURL, nil
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("no peer has model %s@%s (last error: %w)", repoID, revision, lastErr)
	}
	return "", fmt.Errorf("no peer has model %s@%s", repoID, revision)
}

// PeerHasFile checks whether a peer can serve a specific snapshot file (via manifest).
func (c *Client) PeerHasFile(baseURL, repoID, revision, filename string) (blobHash string, size int64, ok bool, err error) {
	m, err := c.FetchManifest(baseURL, repoID, revision)
	if err != nil {
		return "", 0, false, err
	}
	filename = strings.TrimPrefix(filename, "/")
	for _, f := range m.Files {
		if f.Path == filename {
			return f.Blob, f.Size, true, nil
		}
	}
	return "", 0, false, nil
}
