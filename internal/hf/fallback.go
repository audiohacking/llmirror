package hf

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// Token returns the Hub access token from the environment (HF_TOKEN preferred).
// Also accepts the deprecated HUGGING_FACE_HUB_TOKEN name.
func Token() string {
	if v := os.Getenv("HF_TOKEN"); v != "" {
		return v
	}
	return os.Getenv("HUGGING_FACE_HUB_TOKEN")
}

// Download runs the Hugging Face CLI (`hf download` or legacy `huggingface-cli download`).
// Inherits the process environment so HF_TOKEN / hf auth login credentials apply.
func Download(repoID string, revision string, extraArgs []string) error {
	cmd, name, err := downloadCommand(repoID, revision, extraArgs)
	if err != nil {
		return err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	// Explicit env copy keeps behavior clear; child still sees HF_TOKEN / HF_HOME.
	cmd.Env = os.Environ()

	if Token() == "" && !loggedInHint() {
		log.Printf("llmirror: tip: no HF_TOKEN detected — run `hf auth login` or export HF_TOKEN for gated models and faster authenticated Hub access")
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func downloadCommand(repoID, revision string, extraArgs []string) (*exec.Cmd, string, error) {
	args := []string{"download", repoID}
	if revision != "" && revision != "main" {
		args = append(args, "--revision", revision)
	}
	args = append(args, extraArgs...)

	if path, err := exec.LookPath("hf"); err == nil {
		return exec.Command(path, args...), "hf download", nil
	}
	if path, err := exec.LookPath("huggingface-cli"); err == nil {
		return exec.Command(path, args...), "huggingface-cli download", nil
	}
	return nil, "", fmt.Errorf("hf or huggingface-cli not found in PATH; install huggingface_hub (pip install -U 'huggingface_hub[cli]')")
}

// CLIPath returns which HF CLI binary is available.
func CLIPath() string {
	if path, err := exec.LookPath("hf"); err == nil {
		return path
	}
	if path, err := exec.LookPath("huggingface-cli"); err == nil {
		return path
	}
	return ""
}

// IsHFInvocation checks if argv looks like an HF download command (for proxy/alias mode).
func IsHFInvocation(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	name := strings.ToLower(argv[0])
	if strings.Contains(name, "hf") || strings.Contains(name, "huggingface") {
		return argv[1] == "download"
	}
	return false
}

// loggedInHint is a best-effort check for a cached Hub token file.
func loggedInHint() bool {
	if home := os.Getenv("HF_HOME"); home != "" {
		if _, err := os.Stat(strings.TrimRight(home, "/") + "/token"); err == nil {
			return true
		}
	}
	if p := os.Getenv("HF_TOKEN_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(home + "/.cache/huggingface/token")
	return err == nil
}
