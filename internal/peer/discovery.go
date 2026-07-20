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
	txtGroupKey = "group"
	txtVersion  = "v=1"
)

// Discovery advertises this node and finds peers via mDNS.
type Discovery struct {
	Instance string
	Port     int
	Group    string // when set, only advertise/browse peers in this group
	Peers    chan string
	server   *zeroconf.Server
}

func NewDiscovery(instance string, port int, group string) *Discovery {
	return &Discovery{
		Instance: instance,
		Port:     port,
		Group:    strings.TrimSpace(group),
		Peers:    make(chan string, 16),
	}
}

func (d *Discovery) Advertise() error {
	host, _ := os.Hostname()
	txt := []string{txtVersion}
	if d.Group != "" {
		txt = append(txt, txtGroupKey+"="+d.Group)
	}
	srv, err := zeroconf.Register(d.Instance, serviceType, domain, d.Port, txt, nil)
	if err != nil {
		return fmt.Errorf("mdns advertise: %w", err)
	}
	d.server = srv
	if d.Group != "" {
		log.Printf("llmirror: advertising %s group=%q on port %d (host %s)", serviceType, d.Group, d.Port, host)
	} else {
		log.Printf("llmirror: advertising %s on port %d (host %s) — no group set (visible to all llmirror browsers)", serviceType, d.Port, host)
	}
	return nil
}

func (d *Discovery) Browse(ctx context.Context) ([]string, error) {
	entries := make(chan *zeroconf.ServiceEntry)
	var peers []string
	seen := map[string]struct{}{}

	go func() {
		for entry := range entries {
			if !groupMatch(d.Group, entry.Text) {
				continue
			}
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

// groupMatch returns true when local group is empty (accept all) or TXT group equals local.
// Peers advertising no group are only accepted when local group is also empty.
func groupMatch(localGroup string, txt []string) bool {
	remote := txtValue(txt, txtGroupKey)
	if localGroup == "" {
		return true
	}
	return remote == localGroup
}

func txtValue(txt []string, key string) string {
	prefix := key + "="
	for _, t := range txt {
		if strings.HasPrefix(t, prefix) {
			return strings.TrimPrefix(t, prefix)
		}
	}
	return ""
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

// DiscoverPeers merges mDNS results (group-filtered) with the static peer list.
// Static peers are still verified at request time via the group HTTP header.
func DiscoverPeers(staticFile string, timeout time.Duration, group string) ([]string, error) {
	static, err := StaticPeers(staticFile)
	if err != nil {
		return nil, err
	}

	d := NewDiscovery("llmirror", 0, group)
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
