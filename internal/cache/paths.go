package cache

import (
	"os"
	"path/filepath"
	"runtime"
)

// HubDir returns the Hugging Face Hub cache directory, matching huggingface_hub defaults:
// HF_HUB_CACHE > HF_HOME/hub > XDG_CACHE_HOME/huggingface/hub > ~/.cache/huggingface/hub
func HubDir() (string, error) {
	if v := os.Getenv("HF_HUB_CACHE"); v != "" {
		return expandHome(v), nil
	}
	if v := os.Getenv("HF_HOME"); v != "" {
		return filepath.Join(expandHome(v), "hub"), nil
	}
	if runtime.GOOS != "windows" {
		if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
			return filepath.Join(expandHome(xdg), "huggingface", "hub"), nil
		}
	}
	return filepath.Join(expandHome("~"), ".cache", "huggingface", "hub"), nil
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	return path
}
