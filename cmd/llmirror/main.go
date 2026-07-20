package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lmangani/llmirror/internal/cache"
	"github.com/lmangani/llmirror/internal/cdnproxy"
	"github.com/lmangani/llmirror/internal/download"
	"github.com/lmangani/llmirror/internal/hf"
	"github.com/lmangani/llmirror/internal/peer"
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
		Long: `llmirror shares Hugging Face hub cache between hosts on your network.

Before hitting huggingface.co, it checks the local HF cache and peers on the LAN.
Models land in the same paths as huggingface-cli (HF_HUB_CACHE / ~/.cache/huggingface/hub).

Run 'llmirror serve' on each host, then use 'llmirror download org/model' or alias hf to llmirror.`,
	}

	root.AddCommand(
		cmdDownload(),
		cmdServe(),
		cmdCDNProxy(),
		cmdScan(),
		cmdPeers(),
		cmdProxy(),
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
	var revision string
	var peersFile string
	var skipPeers bool
	var skipHF bool

	cmd := &cobra.Command{
		Use:   "download REPO_ID [HF_FLAGS...]",
		Short: "Download a model (local → peers → Hugging Face)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir, err := cache.HubDir()
			if err != nil {
				return err
			}
			repoID := args[0]
			extra := args[1:]
			return download.Resolve(download.Options{
				HubDir:      hubDir,
				RepoID:      repoID,
				Revision:    revision,
				PeersFile:   peersFile,
				SkipPeers:   skipPeers,
				SkipHF:      skipHF,
				HFExtraArgs: extra,
			})
		},
	}
	cmd.Flags().StringVar(&revision, "revision", "main", "Model revision (branch, tag, or commit)")
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list (one URL per line)")
	cmd.Flags().BoolVar(&skipPeers, "skip-peers", false, "Do not query fleet peers")
	cmd.Flags().BoolVar(&skipHF, "skip-hf", false, "Fail instead of falling back to Hugging Face")
	return cmd
}

func cmdServe() *cobra.Command {
	var addr string
	var peersFile string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve local HF cache to peers on the network",
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir, err := cache.HubDir()
			if err != nil {
				return err
			}
			if addr == "" {
				addr = ":7947"
			}
			host, portStr, _ := strings.Cut(addr, ":")
			if host == "" {
				host = "0.0.0.0"
			}
			port, _ := strconv.Atoi(portStr)

			d := peer.NewDiscovery(hostname(), port)
			if err := d.Advertise(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: mDNS advertise failed: %v\n", err)
			}
			defer d.Shutdown()

			fmt.Printf("llmirror: serving %s on http://%s:%d\n", hubDir, listenHost(host), port)
			if peersFile != "" {
				fmt.Printf("llmirror: static peers file: %s\n", peersFile)
			}
			return (&peer.Server{HubDir: hubDir, Addr: addr}).ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":7947", "Listen address (default :7947)")
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list path")
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
				fmt.Printf("%s  [%s]\n", m.RepoID, strings.Join(m.Revisions, ", "))
			}
			return nil
		},
	}
}

func cmdPeers() *cobra.Command {
	var peersFile string

	return &cobra.Command{
		Use:   "peers",
		Short: "Discover llmirror peers on the network",
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := peer.DiscoverPeers(peersFile, peerBrowseTimeout())
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
}

func cmdCDNProxy() *cobra.Command {
	var addr string
	var peersFile string
	var upstream string
	var skipPeers bool

	cmd := &cobra.Command{
		Use:   "cdn-proxy",
		Short: "HF_ENDPOINT reverse proxy (local → peers → huggingface.co)",
		Long: `Run a reverse proxy compatible with huggingface_hub's HF_ENDPOINT.

Point Python libraries at this proxy so resolve/raw downloads try the local
HF cache and fleet peers before huggingface.co:

  export HF_ENDPOINT=http://127.0.0.1:7950
  llmirror cdn-proxy --addr :7950

API and non-model traffic is forwarded upstream unchanged.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir, err := cache.HubDir()
			if err != nil {
				return err
			}
			if addr == "" {
				addr = ":7950"
			}
			return cdnproxy.New(cdnproxy.Config{
				HubDir:    hubDir,
				Addr:      addr,
				Upstream:  upstream,
				PeersFile: peersFile,
				SkipPeers: skipPeers,
			}).ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":7950", "Listen address")
	cmd.Flags().StringVar(&upstream, "upstream", "https://huggingface.co", "Upstream Hub endpoint")
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list")
	cmd.Flags().BoolVar(&skipPeers, "skip-peers", false, "Do not query fleet peers")
	return cmd
}

// cmdProxy implements drop-in replacement: llmirror proxy -- hf download ...
func cmdProxy() *cobra.Command {
	var peersFile string

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
					HubDir:    hubDir,
					RepoID:    repoID,
					Revision:  revision,
					PeersFile: peersFile,
					HFExtraArgs: extra,
				})
			}
			// Not a download: pass through to real hf binary.
			path := hf.CLIPath()
			if path == "" {
				return fmt.Errorf("hf not found")
			}
			return passthrough(path, args)
		},
	}
	cmd.Flags().StringVar(&peersFile, "peers-file", defaultPeersFile(), "Static peer list")
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
	return home + "/.config/llmirror/peers"
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

func peerBrowseTimeout() time.Duration {
	return 3 * time.Second
}
