package hf

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Download runs the Hugging Face CLI (`hf download` or legacy `huggingface-cli download`).
func Download(repoID string, revision string, extraArgs []string) error {
	cmd, name, err := downloadCommand(repoID, revision, extraArgs)
	if err != nil {
		return err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
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
	return nil, "", fmt.Errorf("hf or huggingface-cli not found in PATH; install huggingface_hub")
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
