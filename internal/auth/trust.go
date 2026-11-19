// Package auth decides who may do what.
//
// Policy (the owner, Nov 18 2026): people on the venue network never type a
// key. Requests from this machine, private LAN ranges and Tailscale are
// trusted with full access. API keys exist for agents (MCP is
// key-only) and for anything outside those networks. A request that
// carries a key is always held to that key's scopes, even on the LAN,
// so an agent can never exceed what its key allows.
package auth

import (
	"net"
	"net/netip"
	"strings"
	"sync"
)

var defaultTrusted = []string{
	"127.0.0.0/8", "::1/128", // this machine
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // private LAN
	"169.254.0.0/16", "fe80::/10", // link-local
	"100.64.0.0/10", "fd7a:115c:a1e0::/48", // Tailscale
	"fc00::/7", // IPv6 unique-local
}

type Trust struct {
	mu       sync.RWMutex
	prefixes []netip.Prefix
}

// NewTrust builds the trusted set. trustLAN=false trusts only this
// machine (plus extra). extra is a comma-separated CIDR list.
func NewTrust(trustLAN bool, extra string) (*Trust, error) {
	t := &Trust{}
	return t, t.Set(trustLAN, extra)
}

// Set replaces the trusted set (Settings → System, applied live).
func (t *Trust) Set(trustLAN bool, extra string) error {
	var prefixes []netip.Prefix
	list := []string{"127.0.0.0/8", "::1/128"}
	if trustLAN {
		list = defaultTrusted
	}
	for _, e := range strings.Split(extra, ",") {
		if e = strings.TrimSpace(e); e != "" {
			list = append(list, e)
		}
	}
	for _, c := range list {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return err
		}
		prefixes = append(prefixes, p)
	}
	t.mu.Lock()
	t.prefixes = prefixes
	t.mu.Unlock()
	return nil
}

// Trusted reports whether a request's remote address (host:port) is
// inside the trusted networks.
func (t *Trust) Trusted(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, p := range t.prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
