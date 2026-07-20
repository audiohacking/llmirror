package cache

import (
	"fmt"
	"strings"
)

const modelPrefix = "models--"

// RepoFolderName converts "meta-llama/Llama-2-7b-hf" to "models--meta-llama--Llama-2-7b-hf".
func RepoFolderName(repoID string) string {
	return modelPrefix + strings.ReplaceAll(repoID, "/", "--")
}

// RepoIDFromFolder converts "models--meta-llama--Llama-2-7b-hf" back to "meta-llama/Llama-2-7b-hf".
func RepoIDFromFolder(folder string) (string, error) {
	if !strings.HasPrefix(folder, modelPrefix) {
		return "", fmt.Errorf("not a model cache folder: %s", folder)
	}
	body := strings.TrimPrefix(folder, modelPrefix)
	parts := strings.Split(body, "--")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid model cache folder: %s", folder)
	}
	org := parts[0]
	name := strings.Join(parts[1:], "--")
	return org + "/" + name, nil
}
