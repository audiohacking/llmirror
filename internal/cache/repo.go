package cache

import (
	"fmt"
	"strings"
)

// RepoType is a Hugging Face hub repository kind.
type RepoType string

const (
	RepoModel   RepoType = "model"
	RepoDataset RepoType = "dataset"
	RepoSpace   RepoType = "space"
)

func (t RepoType) Prefix() string {
	switch t {
	case RepoDataset:
		return "datasets--"
	case RepoSpace:
		return "spaces--"
	default:
		return "models--"
	}
}

func (t RepoType) String() string {
	if t == "" {
		return string(RepoModel)
	}
	return string(t)
}

// ParseRepoType normalizes user/API input.
func ParseRepoType(s string) RepoType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "dataset", "datasets":
		return RepoDataset
	case "space", "spaces":
		return RepoSpace
	default:
		return RepoModel
	}
}

// RepoFolderName converts repo id + type to HF cache folder name.
// Example: model "meta-llama/Llama-2-7b-hf" → "models--meta-llama--Llama-2-7b-hf".
func RepoFolderName(repoID string, repoType ...RepoType) string {
	t := RepoModel
	if len(repoType) > 0 && repoType[0] != "" {
		t = repoType[0]
	}
	return t.Prefix() + strings.ReplaceAll(repoID, "/", "--")
}

// RepoIDFromFolder converts a cache folder name back to repo id and type.
func RepoIDFromFolder(folder string) (repoID string, repoType RepoType, err error) {
	var prefix string
	switch {
	case strings.HasPrefix(folder, "models--"):
		prefix, repoType = "models--", RepoModel
	case strings.HasPrefix(folder, "datasets--"):
		prefix, repoType = "datasets--", RepoDataset
	case strings.HasPrefix(folder, "spaces--"):
		prefix, repoType = "spaces--", RepoSpace
	default:
		return "", "", fmt.Errorf("not a hub cache folder: %s", folder)
	}
	body := strings.TrimPrefix(folder, prefix)
	parts := strings.Split(body, "--")
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "") {
		return "", "", fmt.Errorf("invalid hub cache folder: %s", folder)
	}
	if len(parts) == 1 {
		return parts[0], repoType, nil
	}
	org := parts[0]
	name := strings.Join(parts[1:], "--")
	return org + "/" + name, repoType, nil
}
