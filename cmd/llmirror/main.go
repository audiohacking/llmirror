package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lmangani/llmirror/internal/auth"
	"github.com/lmangani/llmirror/internal/cache"
	"github.com/lmangani/llmirror/internal/cdnproxy"
	"github.com/lmangani/llmirror/internal/download"
	"github.com/lmangani/llmirror/internal/hf"
	"github.com/lmangani/llmirror/internal/netacl"
	"github.com/lmangani/llmirror/internal/peer"
	"github.com/lmangani/llmirror/internal/service"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	if err := newRoot().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "llmirror",
		Short: "P2P mirror for Hugging Face model cache across your fleet",
		Long: `llmirror shares Hugging Face hub cache between hosts on your LAN.

Secure by default: only private/local clients are accepted, optional shared
fleet token, and install-service wires systemd/launchd with those defaults.`,
	}

	root.AddCommand(
		cmdDownload(),
		cmdServe(),
		cmdCDNProxy(),
		cmdScan(),
		cmdPeers(),
		cmdProxy(),
		cmdInstallService(),
		cmdUninstallService(),
		&cobra.Command{
			Use:   "version",
			Short: "Print version",
			Run: func(cmd *cobra.Command, args []string) {
				fmt.Println(version)
			},
		},
	)
	return root
}

func cmdDownload() *cobra.Command {
	var revision, peersFile, repoType, token, tokenFile, group, groupFile string
	var skipPeers, skipHF bool

	cmd := &cobra.Command{
		Use:   "download REPO_ID [HF_FLAGS...]",
		Short: "Download a model (local → peers → Hugging Face)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir, err := cache.HubDir()
			if err != nil {
				return err
			}
			tok, err := auth.LoadToken(token, tokenFile)
			if err != nil {
				return err
			}
			grp, err := auth.LoadGroup(group, groupFile)
			if err != nil {
				return err
			}
			return download.Resolve(download.Options{
				HubDir:      hubDir,
				RepoID:      args[0],
				RepoType:    cache.ParseRepoType(repoType),
				Revision:    revision,
				PeersFile:   peersFile,
				SkipPeers:   skipPeers,
				SkipHF:      skipHF,
				Token:       tok,
				Group:       grp,
				HFExtraArgs: args[1:],
			})
		},
	}
	cmd.Flags().StringVar(&revision, "revision", "main", "Model revision (branch, tag, or commit)")
	cmd.Flags().StringVar(&repoType, "repo-type", "model", "Repo type: model, dataset, or space")
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list (one URL per line)")
	cmd.Flags().StringVar(&token, "token", "", "Fleet shared token (or LLMIRROR_TOKEN)")
	cmd.Flags().StringVar(&tokenFile, "token-file", defaultTokenFile(), "Fleet token file")
	cmd.Flags().StringVar(&group, "group", "", "Fleet group id (or LLMIRROR_GROUP)")
	cmd.Flags().StringVar(&groupFile, "group-file", defaultGroupFile(), "Fleet group file")
	cmd.Flags().BoolVar(&skipPeers, "skip-peers", false, "Do not query fleet peers")
	cmd.Flags().BoolVar(&skipHF, "skip-hf", false, "Fail instead of falling back to Hugging Face")
	return cmd
}

func cmdServe() *cobra.Command {
	var addr, peersFile, token, tokenFile, group, groupFile, allow string
	var allowPublic bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve local HF cache to LAN peers (private networks only)",
		Long: `Expose the local Hugging Face hub cache to other hosts on your private network.

Security defaults:
  • Only RFC1918 / loopback / link-local clients are accepted
  • X-Forwarded-For is ignored (cannot spoof client IP via proxy headers)
  • Fleet group (--group) isolates mDNS discovery and HTTP access
  • Optional shared token (--token / LLMIRROR_TOKEN) required when configured
  • Use --allow to add extra private CIDRs; --allow-public is an explicit escape hatch`,
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir, err := cache.HubDir()
			if err != nil {
				return err
			}
			if addr == "" {
				addr = ":7947"
			}
			tok, err := auth.LoadToken(token, tokenFile)
			if err != nil {
				return err
			}
			grp, err := auth.LoadGroup(group, groupFile)
			if err != nil {
				return err
			}

			var acl *netacl.ACL
			if allowPublic {
				fmt.Fprintln(os.Stderr, "WARNING: --allow-public disables network ACL; anyone who can reach this port can read your HF cache")
			} else {
				cidrs := netacl.ParseAllowList(allow)
				acl, err = netacl.New(cidrs)
				if err != nil {
					return err
				}
			}

			host, portStr, _ := strings.Cut(normalizeListen(addr), ":")
			if host == "" {
				host = "0.0.0.0"
			}
			port, _ := strconv.Atoi(portStr)

			d := peer.NewDiscovery(hostname(), port, grp)
			if err := d.Advertise(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: mDNS advertise failed: %v\n", err)
			}
			defer d.Shutdown()

			fmt.Printf("llmirror: serving %s on http://%s:%d\n", hubDir, listenHost(host), port)
			if grp != "" {
				fmt.Printf("llmirror: fleet group %q\n", grp)
			} else {
				fmt.Fprintln(os.Stderr, "warning: no fleet group set; mDNS peers from any group are visible (set --group or LLMIRROR_GROUP)")
			}
			if tok != "" {
				fmt.Println("llmirror: fleet token auth enabled")
			} else {
				fmt.Fprintln(os.Stderr, "warning: no fleet token set; any host on your LAN (same group) can pull models (set --token-file or LLMIRROR_TOKEN)")
			}
			if !allowPublic {
				fmt.Println("llmirror: network ACL = private/local only")
			}
			return (&peer.Server{HubDir: hubDir, Addr: addr, Token: tok, Group: grp, ACL: acl}).ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":7947", "Listen address")
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list path")
	cmd.Flags().StringVar(&token, "token", "", "Fleet shared token")
	cmd.Flags().StringVar(&tokenFile, "token-file", defaultTokenFile(), "Fleet token file")
	cmd.Flags().StringVar(&group, "group", "", "Fleet group id")
	cmd.Flags().StringVar(&groupFile, "group-file", defaultGroupFile(), "Fleet group file")
	cmd.Flags().StringVar(&allow, "allow", "", "Extra allowed CIDRs (comma-separated); default is private nets only")
	cmd.Flags().BoolVar(&allowPublic, "allow-public", false, "Disable network ACL (dangerous)")
	return cmd
}

func cmdCDNProxy() *cobra.Command {
	var addr, peersFile, upstream, token, tokenFile, group, groupFile, allow string
	var skipPeers, allowPublic bool

	cmd := &cobra.Command{
		Use:   "cdn-proxy",
		Short: "HF_ENDPOINT reverse proxy (default: loopback only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir, err := cache.HubDir()
			if err != nil {
				return err
			}
			if addr == "" {
				addr = "127.0.0.1:7950"
			}
			tok, err := auth.LoadToken(token, tokenFile)
			if err != nil {
				return err
			}
			grp, err := auth.LoadGroup(group, groupFile)
			if err != nil {
				return err
			}
			var acl *netacl.ACL
			if !allowPublic {
				cidrs := netacl.ParseAllowList(allow)
				if len(cidrs) == 0 {
					cidrs = []string{"127.0.0.0/8", "::1/128"}
				}
				acl, err = netacl.New(cidrs)
				if err != nil {
					return err
				}
			} else {
				fmt.Fprintln(os.Stderr, "WARNING: cdn-proxy --allow-public disables ACL")
			}
			return cdnproxy.New(cdnproxy.Config{
				HubDir:    hubDir,
				Addr:      addr,
				Upstream:  upstream,
				PeersFile: peersFile,
				SkipPeers: skipPeers,
				Token:     tok,
				Group:     grp,
				ACL:       acl,
			}).ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7950", "Listen address (loopback by default)")
	cmd.Flags().StringVar(&upstream, "upstream", "https://huggingface.co", "Upstream Hub endpoint")
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list")
	cmd.Flags().StringVar(&token, "token", "", "Fleet token for peer fetches")
	cmd.Flags().StringVar(&tokenFile, "token-file", defaultTokenFile(), "Fleet token file")
	cmd.Flags().StringVar(&group, "group", "", "Fleet group id")
	cmd.Flags().StringVar(&groupFile, "group-file", defaultGroupFile(), "Fleet group file")
	cmd.Flags().StringVar(&allow, "allow", "", "Allowed CIDRs (default: loopback only)")
	cmd.Flags().BoolVar(&allowPublic, "allow-public", false, "Disable network ACL (dangerous)")
	cmd.Flags().BoolVar(&skipPeers, "skip-peers", false, "Do not query fleet peers")
	return cmd
}

func cmdScan() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "List models in the local HF cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir, err := cache.HubDir()
			if err != nil {
				return err
			}
			fmt.Printf("cache: %s\n\n", hubDir)
			models, err := cache.ScanModels(hubDir)
			if err != nil {
				return err
			}
			if len(models) == 0 {
				fmt.Println("(no models cached)")
				return nil
			}
			for _, m := range models {
				fmt.Printf("%s  (%s)  [%s]\n", m.RepoID, m.RepoType, strings.Join(m.Revisions, ", "))
			}
			return nil
		},
	}
}

func cmdPeers() *cobra.Command {
	var peersFile, group, groupFile string
	cmd := &cobra.Command{
		Use:   "peers",
		Short: "Discover llmirror peers on the network",
		RunE: func(cmd *cobra.Command, args []string) error {
			grp, err := auth.LoadGroup(group, groupFile)
			if err != nil {
				return err
			}
			list, err := peer.DiscoverPeers(peersFile, peerBrowseTimeout(), grp)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("(no peers found)")
				return nil
			}
			for _, p := range list {
				fmt.Println(p)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list")
	cmd.Flags().StringVar(&group, "group", "", "Fleet group id")
	cmd.Flags().StringVar(&groupFile, "group-file", defaultGroupFile(), "Fleet group file")
	return cmd
}

func cmdProxy() *cobra.Command {
	var peersFile, token, tokenFile, group, groupFile string
	cmd := &cobra.Command{
		Use:   "proxy -- COMMAND [ARGS...]",
		Short: "Intercept HF download commands (use as hf alias)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("usage: llmirror proxy -- hf download org/model")
			}
			if hf.IsHFInvocation(args) && len(args) >= 3 {
				hubDir, err := cache.HubDir()
				if err != nil {
					return err
				}
				tok, err := auth.LoadToken(token, tokenFile)
				if err != nil {
					return err
				}
				grp, err := auth.LoadGroup(group, groupFile)
				if err != nil {
					return err
				}
				repoID := args[2]
				revision := "main"
				var extra []string
				for i := 3; i < len(args); i++ {
					if args[i] == "--revision" && i+1 < len(args) {
						revision = args[i+1]
						i++
						continue
					}
					extra = append(extra, args[i])
				}
				return download.Resolve(download.Options{
					HubDir:      hubDir,
					RepoID:      repoID,
					Revision:    revision,
					PeersFile:   peersFile,
					Token:       tok,
					Group:       grp,
					HFExtraArgs: extra,
				})
			}
			path := hf.CLIPath()
			if path == "" {
				return fmt.Errorf("hf not found")
			}
			return passthrough(path, args)
		},
	}
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list")
	cmd.Flags().StringVar(&token, "token", "", "Fleet shared token")
	cmd.Flags().StringVar(&tokenFile, "token-file", defaultTokenFile(), "Fleet token file")
	cmd.Flags().StringVar(&group, "group", "", "Fleet group id")
	cmd.Flags().StringVar(&groupFile, "group-file", defaultGroupFile(), "Fleet group file")
	return cmd
}

func cmdInstallService() *cobra.Command {
	var addr, allow, group string
	var system bool
	cmd := &cobra.Command{
		Use:   "install-service",
		Short: "Install llmirror serve as a background service (systemd / launchd)",
		Long: `Installs a user-level service by default (systemd --user on Linux, LaunchAgent on macOS).

Generates ~/.config/llmirror/token and ~/.config/llmirror/group if missing,
and enables private-network-only serve scoped to that fleet group.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			bin, _ = filepath.Abs(bin)
			return service.Install(service.Options{
				BinaryPath: bin,
				Addr:       addr,
				AllowCIDRs: allow,
				Group:      group,
				UserScope:  !system,
			})
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":7947", "Listen address for the service")
	cmd.Flags().StringVar(&allow, "allow", "", "Extra allowed CIDRs")
	cmd.Flags().StringVar(&group, "group", "", "Fleet group id (generated if omitted)")
	cmd.Flags().BoolVar(&system, "system", false, "Install system-wide unit (requires root on Linux)")
	return cmd
}

func cmdUninstallService() *cobra.Command {
	var system bool
	cmd := &cobra.Command{
		Use:   "uninstall-service",
		Short: "Remove the background service",
		RunE: func(cmd *cobra.Command, args []string) error {
			return service.Uninstall(!system)
		},
	}
	cmd.Flags().BoolVar(&system, "system", false, "Remove system-wide unit")
	return cmd
}

func passthrough(path string, args []string) error {
	if len(args) > 0 && args[0] == "hf" {
		args = args[1:]
	}
	c := exec.Command(path, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

func defaultPeersFile() string {
	if v := os.Getenv("LLMIRROR_PEERS"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "llmirror", "peers")
}

func defaultTokenFile() string {
	if v := os.Getenv("LLMIRROR_TOKEN_FILE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".config", "llmirror", "token")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func defaultGroupFile() string {
	if v := os.Getenv("LLMIRROR_GROUP_FILE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".config", "llmirror", "group")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "llmirror"
	}
	return h
}

func listenHost(bind string) string {
	if bind == "0.0.0.0" || bind == "" {
		return "0.0.0.0"
	}
	return bind
}

func normalizeListen(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "0.0.0.0" + addr
	}
	return addr
}

func peerBrowseTimeout() time.Duration {
	return 3 * time.Second
}
