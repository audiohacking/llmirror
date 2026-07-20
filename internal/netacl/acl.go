package netacl

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
)

// DefaultPrivateCIDRs are the networks allowed to reach llmirror serve by default.
// Public Internet clients are rejected unless explicitly overridden.
var DefaultPrivateCIDRs = []string{
	"127.0.0.0/8",    // IPv4 loopback
	"10.0.0.0/8",     // RFC1918
	"172.16.0.0/12",  // RFC1918
	"192.168.0.0/16", // RFC1918
	"169.254.0.0/16", // IPv4 link-local
	"::1/128",        // IPv6 loopback
	"fc00::/7",       // IPv6 unique local
	"fe80::/10",      // IPv6 link-local
}

// ACL decides whether a remote IP may access the server.
type ACL struct {
	nets []*net.IPNet
}

// New parses CIDR allowlist entries. Empty list uses DefaultPrivateCIDRs.
func New(cidrs []string) (*ACL, error) {
	if len(cidrs) == 0 {
		cidrs = DefaultPrivateCIDRs
	}
	a := &ACL{}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			// Allow bare IP → /32 or /128
			ip := net.ParseIP(c)
			if ip == nil {
				return nil, fmt.Errorf("invalid allow CIDR %q: %w", c, err)
			}
			if ip.To4() != nil {
				_, n, err = net.ParseCIDR(c + "/32")
			} else {
				_, n, err = net.ParseCIDR(c + "/128")
			}
			if err != nil {
				return nil, fmt.Errorf("invalid allow IP %q: %w", c, err)
			}
		}
		a.nets = append(a.nets, n)
	}
	if len(a.nets) == 0 {
		return New(DefaultPrivateCIDRs)
	}
	return a, nil
}

// Allows reports whether ip is inside the allowlist.
func (a *ACL) Allows(ip net.IP) bool {
	if a == nil || ip == nil {
		return false
	}
	for _, n := range a.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Middleware rejects requests whose TCP peer is outside the allowlist.
// X-Forwarded-For / X-Real-IP are intentionally ignored so proxies cannot spoof access.
func (a *ACL) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, err := clientIP(r)
		if err != nil || !a.Allows(ip) {
			log.Printf("llmirror: rejected non-local client %v", ip)
			http.Error(w, "forbidden: only local/private networks are allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) (net.IP, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may be bare IP in tests
		ip := net.ParseIP(r.RemoteAddr)
		if ip == nil {
			return nil, fmt.Errorf("bad remote addr %q", r.RemoteAddr)
		}
		return ip, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("bad remote host %q", host)
	}
	return ip, nil
}

// IsPublicBind reports whether addr listens on all interfaces (internet-reachable if firewall allows).
func IsPublicBind(addr string) bool {
	host, _, err := net.SplitHostPort(normalizeAddr(addr))
	if err != nil {
		host = addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsUnspecified()
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "0.0.0.0" + addr
	}
	return addr
}

// ParseAllowList splits comma/space separated CIDRs.
func ParseAllowList(s string) []string {
	if s == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	var out []string
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
