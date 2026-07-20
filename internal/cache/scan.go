package cache

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Model holds metadata about a cached Hugging Face hub repository.
type Model struct {
	RepoID    string            `json:"repo_id"`
	RepoType  string            `json:"repo_type"`
	Folder    string            `json:"folder"`
	Revisions []string          `json:"revisions"`
	Refs      map[string]string `json:"refs"`
}

// ScanModels lists hub repositories (models, datasets, spaces) in the HF cache.
func ScanModels(hubDir string) ([]Model, error) {
	entries, err := os.ReadDir(hubDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var models []Model
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoID, repoType, err := RepoIDFromFolder(entry.Name())
		if err != nil {
			continue
		}
		repoDir := filepath.Join(hubDir, entry.Name())
		revisions, err := listRevisions(repoDir)
		if err != nil || len(revisions) == 0 {
			continue
		}
		refs, _ := listRefs(repoDir)
		models = append(models, Model{
			RepoID:    repoID,
			RepoType:  repoType.String(),
			Folder:    entry.Name(),
			Revisions: revisions,
			Refs:      refs,
		})
	}
	return models, nil
}

func listRevisions(repoDir string) ([]string, error) {
	snapDir := filepath.Join(repoDir, "snapshots")
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		return nil, err
	}
	var revs []string
	for _, e := range entries {
		if e.IsDir() {
			revs = append(revs, e.Name())
		}
	}
	return revs, nil
}

func listRefs(repoDir string) (map[string]string, error) {
	refsDir := filepath.Join(repoDir, "refs")
	entries, err := os.ReadDir(refsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	refs := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(refsDir, e.Name()))
		if err != nil {
			continue
		}
		hash := strings.TrimSpace(string(data))
		if hash != "" {
			refs[e.Name()] = hash
		}
	}
	return refs, nil
}

// ResolveRevision returns the commit hash for a named ref or the hash itself.
func ResolveRevision(repoDir, revision string) (string, error) {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		revision = "main"
	}

	if _, err := os.Stat(filepath.Join(repoDir, "snapshots", revision)); err == nil {
		return revision, nil
	}

	refPath := filepath.Join(repoDir, "refs", revision)
	if data, err := os.ReadFile(refPath); err == nil {
		hash := strings.TrimSpace(string(data))
		if hash == "" {
			return "", fmt.Errorf("empty ref for revision %q", revision)
		}
		if _, err := os.Stat(filepath.Join(repoDir, "snapshots", hash)); err == nil {
			return hash, nil
		}
		return "", fmt.Errorf("ref %q points to %s but snapshot is missing", revision, hash)
	}

	if len(revision) >= 7 && len(revision) < 40 {
		revs, err := listRevisions(repoDir)
		if err == nil {
			var matches []string
			for _, r := range revs {
				if strings.HasPrefix(r, revision) {
					matches = append(matches, r)
				}
			}
			if len(matches) == 1 {
				return matches[0], nil
			}
			if len(matches) > 1 {
				return "", fmt.Errorf("ambiguous revision prefix %q", revision)
			}
		}
	}

	return "", fmt.Errorf("revision %q not found in cache", revision)
}

// HasRevision reports whether the repo revision resolves and has at least one snapshot file.
func HasRevision(hubDir, repoID, revision string, repoType ...RepoType) (bool, error) {
	t := RepoModel
	if len(repoType) > 0 {
		t = repoType[0]
	}
	repoDir := filepath.Join(hubDir, RepoFolderName(repoID, t))
	if _, err := os.Stat(repoDir); err != nil {
		return false, nil
	}
	hash, err := ResolveRevision(repoDir, revision)
	if err != nil {
		return false, nil
	}
	snapDir := filepath.Join(repoDir, "snapshots", hash)
	if _, err := os.Stat(snapDir); err != nil {
		return false, nil
	}
	var found bool
	err = filepath.WalkDir(snapDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found && err == nil, nil
}

// ResolveFilePath returns the on-disk path for a file inside a snapshot.
func ResolveFilePath(hubDir, repoID, revision, filename string, repoType ...RepoType) (absPath string, blobHash string, size int64, err error) {
	t := RepoModel
	if len(repoType) > 0 {
		t = repoType[0]
	}
	repoDir := filepath.Join(hubDir, RepoFolderName(repoID, t))
	hash, err := ResolveRevision(repoDir, revision)
	if err != nil {
		return "", "", 0, err
	}
	filePath := filepath.Join(repoDir, "snapshots", hash, filepath.FromSlash(filename))
	blobHash, size, err = resolveBlob(repoDir, filePath)
	if err != nil {
		return "", "", 0, err
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return "", "", 0, err
	}
	target := filePath
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(filePath)
		if err != nil {
			return "", "", 0, err
		}
		if !filepath.IsAbs(link) {
			target = filepath.Join(filepath.Dir(filePath), link)
		} else {
			target = link
		}
	}
	return target, blobHash, size, nil
}

// ManifestEntry describes one file in a snapshot.
type ManifestEntry struct {
	Path string `json:"path"`
	Blob string `json:"blob"`
	Size int64  `json:"size"`
}

// SnapshotManifest walks a snapshot directory and returns file paths with blob hashes.
func SnapshotManifest(repoDir, revisionHash string) ([]ManifestEntry, error) {
	snapDir := filepath.Join(repoDir, "snapshots", revisionHash)
	var entries []ManifestEntry

	err := filepath.WalkDir(snapDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(snapDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		blobHash, size, err := resolveBlob(repoDir, path)
		if err != nil {
			return err
		}
		entries = append(entries, ManifestEntry{
			Path: rel,
			Blob: blobHash,
			Size: size,
		})
		return nil
	})
	return entries, err
}

func resolveBlob(repoDir, filePath string) (hash string, size int64, err error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return "", 0, err
	}
	target := filePath
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(filePath)
		if err != nil {
			return "", 0, err
		}
		if !filepath.IsAbs(link) {
			target = filepath.Join(filepath.Dir(filePath), link)
		} else {
			target = link
		}
	}
	blobInfo, err := os.Stat(target)
	if err != nil {
		return "", 0, err
	}
	hash = filepath.Base(target)
	return hash, blobInfo.Size(), nil
}
