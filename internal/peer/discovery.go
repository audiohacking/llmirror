package peer

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	serviceType = "_llmirror._tcp"
	domain      = "local."
)

// Discovery advertises this node and finds peers via mDNS.
type Discovery struct {
	Instance string
	Port     int
	Peers    chan string
	server   *zeroconf.Server
}

func NewDiscovery(instance string, port int) *Discovery {
	return &Discovery{
		Instance: instance,
		Port:     port,
		Peers:    make(chan string, 16),
	}
}

func (d *Discovery) Advertise() error {
	host, _ := os.Hostname()
	txt := []string{"v=1"}
	srv, err := zeroconf.Register(d.Instance, serviceType, domain, d.Port, txt, nil)
	if err != nil {
		return fmt.Errorf("mdns advertise: %w", err)
	}
	d.server = srv
	log.Printf("llmirror: advertising %s on %s port %d (host %s)", serviceType, domain, d.Port, host)
	return nil
}

func (d *Discovery) Browse(ctx context.Context) ([]string, error) {
	entries := make(chan *zeroconf.ServiceEntry)
	var peers []string
	seen := map[string]struct{}{}

	go func() {
		for entry := range entries {
			for _, ip := range entry.AddrIPv4 {
				base := fmt.Sprintf("http://%s:%d", ip.String(), entry.Port)
				if _, ok := seen[base]; ok {
					continue
				}
				seen[base] = struct{}{}
				peers = append(peers, base)
				select {
				case d.Peers <- base:
				default:
				}
			}
		}
	}()

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	if err := resolver.Browse(ctx, serviceType, domain, entries); err != nil {
		return nil, err
	}
	<-ctx.Done()
	return peers, nil
}

func (d *Discovery) Shutdown() {
	if d.server != nil {
		d.server.Shutdown()
	}
}

// StaticPeers reads newline-separated peer base URLs from a file.
func StaticPeers(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var peers []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			peers = append(peers, strings.TrimRight(line, "/"))
		}
	}
	return peers, nil
}

// DiscoverPeers merges mDNS results with static peer list from config file.
func DiscoverPeers(staticFile string, timeout time.Duration) ([]string, error) {
	static, err := StaticPeers(staticFile)
	if err != nil {
		return nil, err
	}

	d := NewDiscovery("llmirror", 0)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	mdns, err := d.Browse(ctx)
	if err != nil {
		mdns = nil
	}

	seen := map[string]struct{}{}
	var all []string
	for _, p := range append(static, mdns...) {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		all = append(all, p)
	}
	return all, nil
}
