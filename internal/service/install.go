package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lmangani/llmirror/internal/auth"
)

// Options for installing the background share daemon.
type Options struct {
	BinaryPath string // absolute path to llmirror
	Addr       string // e.g. :7947
	TokenFile  string
	GroupFile  string
	Group      string // optional explicit group id
	PeersFile  string
	AllowCIDRs string // optional override list
	UserScope  bool   // prefer user systemd / LaunchAgents
}

// Install writes and enables a systemd unit (Linux) or launchd plist (macOS).
func Install(opts Options) error {
	if opts.BinaryPath == "" {
		p, err := os.Executable()
		if err != nil {
			return err
		}
		opts.BinaryPath, _ = filepath.Abs(p)
	}
	if opts.Addr == "" {
		opts.Addr = ":7947"
	}
	if opts.TokenFile == "" {
		opts.TokenFile = defaultTokenFile()
	}
	if opts.GroupFile == "" {
		opts.GroupFile = defaultGroupFile()
	}
	if opts.PeersFile == "" {
		opts.PeersFile = defaultPeersFile()
	}

	if err := os.MkdirAll(filepath.Dir(opts.TokenFile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opts.GroupFile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opts.PeersFile), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(opts.TokenFile); os.IsNotExist(err) {
		tok, err := auth.GenerateToken()
		if err != nil {
			return err
		}
		if err := os.WriteFile(opts.TokenFile, []byte(tok+"\n"), 0o600); err != nil {
			return err
		}
		fmt.Printf("llmirror: generated fleet token at %s (share with other hosts)\n", opts.TokenFile)
	}
	if _, err := os.Stat(opts.GroupFile); os.IsNotExist(err) {
		gid := opts.Group
		if gid == "" {
			var err error
			gid, err = auth.GenerateGroupID()
			if err != nil {
				return err
			}
		}
		if err := os.WriteFile(opts.GroupFile, []byte(gid+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("llmirror: generated fleet group %q at %s (share with other hosts)\n", gid, opts.GroupFile)
	} else if opts.Group != "" {
		fmt.Printf("llmirror: keeping existing group file %s (ignore --group or delete the file to regenerate)\n", opts.GroupFile)
	}
	if _, err := os.Stat(opts.PeersFile); os.IsNotExist(err) {
		_ = os.WriteFile(opts.PeersFile, []byte("# one peer base URL per line, e.g. http://gpu-01:7947\n"), 0o644)
	}

	switch runtime.GOOS {
	case "linux":
		return installSystemd(opts)
	case "darwin":
		return installLaunchd(opts)
	default:
		return fmt.Errorf("install-service is not supported on %s", runtime.GOOS)
	}
}

// Uninstall removes and stops the service unit.
func Uninstall(userScope bool) error {
	switch runtime.GOOS {
	case "linux":
		return uninstallSystemd(userScope)
	case "darwin":
		return uninstallLaunchd(userScope)
	default:
		return fmt.Errorf("uninstall-service is not supported on %s", runtime.GOOS)
	}
}

func defaultTokenFile() string {
	if v := os.Getenv("LLMIRROR_TOKEN_FILE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "llmirror", "token")
}

func defaultGroupFile() string {
	if v := os.Getenv("LLMIRROR_GROUP_FILE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "llmirror", "group")
}

func defaultPeersFile() string {
	if v := os.Getenv("LLMIRROR_PEERS"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "llmirror", "peers")
}

func installSystemd(opts Options) error {
	unit := fmt.Sprintf(`[Unit]
Description=llmirror Hugging Face cache peer
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s serve --addr %s --token-file %s --group-file %s --peers-file %s%s
Restart=on-failure
RestartSec=3
PrivateTmp=true
NoNewPrivileges=true

[Install]
WantedBy=default.target
`,
		shellQuote(opts.BinaryPath),
		shellQuote(opts.Addr),
		shellQuote(opts.TokenFile),
		shellQuote(opts.GroupFile),
		shellQuote(opts.PeersFile),
		allowFlag(opts.AllowCIDRs),
	)

	var unitPath string
	var enableCmd []string
	if opts.UserScope || os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir := filepath.Join(home, ".config", "systemd", "user")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		unitPath = filepath.Join(dir, "llmirror.service")
		enableCmd = []string{"systemctl", "--user", "daemon-reload"}
	} else {
		unitPath = "/etc/systemd/system/llmirror.service"
		enableCmd = []string{"systemctl", "daemon-reload"}
	}

	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	fmt.Printf("llmirror: wrote %s\n", unitPath)

	if err := run(enableCmd...); err != nil {
		return err
	}
	if opts.UserScope || os.Geteuid() != 0 {
		_ = run("systemctl", "--user", "enable", "--now", "llmirror.service")
		fmt.Println("llmirror: enabled user service (systemctl --user status llmirror)")
		fmt.Println("tip: loginctl enable-linger $USER  # keep running after logout")
	} else {
		_ = run("systemctl", "enable", "--now", "llmirror.service")
		fmt.Println("llmirror: enabled system service (systemctl status llmirror)")
	}
	return nil
}

func uninstallSystemd(userScope bool) error {
	if userScope || os.Geteuid() != 0 {
		_ = run("systemctl", "--user", "disable", "--now", "llmirror.service")
		home, _ := os.UserHomeDir()
		_ = os.Remove(filepath.Join(home, ".config", "systemd", "user", "llmirror.service"))
		_ = run("systemctl", "--user", "daemon-reload")
		return nil
	}
	_ = run("systemctl", "disable", "--now", "llmirror.service")
	_ = os.Remove("/etc/systemd/system/llmirror.service")
	_ = run("systemctl", "daemon-reload")
	return nil
}

func installLaunchd(opts Options) error {
	label := "com.audiohacking.llmirror"
	args := []string{
		opts.BinaryPath, "serve",
		"--addr", opts.Addr,
		"--token-file", opts.TokenFile,
		"--group-file", opts.GroupFile,
		"--peers-file", opts.PeersFile,
	}
	if opts.AllowCIDRs != "" {
		args = append(args, "--allow", opts.AllowCIDRs)
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + label + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, a := range args {
		b.WriteString("    <string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString(`  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>EnvironmentVariables</key>
  <dict>
    <key>LLMIRROR_TOKEN_FILE</key>
    <string>` + xmlEscape(opts.TokenFile) + `</string>
    <key>LLMIRROR_GROUP_FILE</key>
    <string>` + xmlEscape(opts.GroupFile) + `</string>
  </dict>
</dict>
</plist>
`)

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(b.String()), 0o644); err != nil {
		return err
	}
	_ = run("launchctl", "unload", plistPath)
	if err := run("launchctl", "load", plistPath); err != nil {
		return err
	}
	fmt.Printf("llmirror: installed LaunchAgent %s\n", plistPath)
	return nil
}

func uninstallLaunchd(userScope bool) error {
	_ = userScope
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	label := "com.audiohacking.llmirror"
	plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	_ = run("launchctl", "unload", plistPath)
	_ = os.Remove(plistPath)
	fmt.Println("llmirror: LaunchAgent removed")
	return nil
}

func allowFlag(cidrs string) string {
	if cidrs == "" {
		return ""
	}
	return " --allow " + shellQuote(cidrs)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

func run(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
