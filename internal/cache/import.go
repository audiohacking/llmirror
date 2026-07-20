package cache

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// BlobFetcher downloads blob bytes starting at offset into w.
// offset > 0 means the caller already has that many bytes in a partial file (HTTP Range).
type BlobFetcher func(blobHash string, offset int64, w io.Writer) error

// ImportSnapshot copies blob files and snapshot layout from a remote manifest into the local HF cache.
// Interrupted blob downloads leave `.partial` files and resume on the next attempt via Range requests.
func ImportSnapshot(hubDir, repoID, revision, revisionHash string, files []ManifestEntry, fetchBlob BlobFetcher) error {
	repoDir := filepath.Join(hubDir, RepoFolderName(repoID))
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "refs"), 0o755); err != nil {
		return err
	}
	snapDir := filepath.Join(repoDir, "snapshots", revisionHash)
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return err
	}

	for _, f := range files {
		blobPath := filepath.Join(repoDir, "blobs", f.Blob)
		if err := ensureBlob(blobPath, f.Size, f.Blob, fetchBlob); err != nil {
			return fmt.Errorf("fetch blob %s: %w", f.Blob, err)
		}
		dest := filepath.Join(snapDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := linkOrCopy(blobPath, dest); err != nil {
			return fmt.Errorf("link %s: %w", f.Path, err)
		}
	}

	refPath := filepath.Join(repoDir, "refs", revision)
	return os.WriteFile(refPath, []byte(revisionHash+"\n"), 0o644)
}

func ensureBlob(path string, expectedSize int64, hash string, fetchBlob BlobFetcher) error {
	if info, err := os.Stat(path); err == nil {
		if expectedSize <= 0 || info.Size() == expectedSize {
			return nil
		}
		// Corrupt complete file — remove and re-fetch.
		_ = os.Remove(path)
	}
	return writeBlobResumable(path, expectedSize, hash, fetchBlob)
}

func writeBlobResumable(path string, expectedSize int64, hash string, fetchBlob BlobFetcher) error {
	tmp := path + ".partial"
	var offset int64

	if info, err := os.Stat(tmp); err == nil {
		offset = info.Size()
		if expectedSize > 0 {
			if offset == expectedSize {
				return os.Rename(tmp, path)
			}
			if offset > expectedSize {
				_ = os.Remove(tmp)
				offset = 0
			}
		}
	}

	var f *os.File
	var err error
	if offset > 0 {
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		f, err = os.Create(tmp)
	}
	if err != nil {
		return err
	}

	fetchErr := fetchBlob(hash, offset, f)
	closeErr := f.Close()
	if fetchErr != nil {
		// Keep .partial for resume on next attempt.
		return fetchErr
	}
	if closeErr != nil {
		return closeErr
	}

	if expectedSize > 0 {
		info, err := os.Stat(tmp)
		if err != nil {
			return err
		}
		if info.Size() != expectedSize {
			return fmt.Errorf("incomplete blob %s: got %d bytes, want %d", hash, info.Size(), expectedSize)
		}
	}
	return os.Rename(tmp, path)
}

func linkOrCopy(src, dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		return copyFile(src, dest)
	}
	if err := os.Symlink(relPath(dest, src), dest); err != nil {
		return copyFile(src, dest)
	}
	return nil
}

func relPath(from, to string) string {
	rel, err := filepath.Rel(filepath.Dir(from), to)
	if err != nil {
		return to
	}
	return rel
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
