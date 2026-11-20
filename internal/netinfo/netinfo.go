// Package netinfo answers "how do phones reach this PC": its LAN
// addresses, and an mDNS responder so <name>.local works.
package netinfo

import (
	"context"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/pion/mdns/v2"
	"golang.org/x/net/ipv4"
)

// Addr is one address phones can use.
type Addr struct {
	IP        string `json:"ip"`
	Interface string `json:"interface"`
	Kind      string `json:"kind"` // lan | tailscale
}

// Addrs lists this PC's IPv4 addresses on private networks and
// Tailscale, LAN first.
func Addrs() []Addr {
	var out []Addr
	ifs, _ := net.Interfaces()
	_, ts, _ := net.ParseCIDR("100.64.0.0/10")
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip := ipn.IP.To4()
			switch {
			case ts.Contains(ip):
				out = append(out, Addr{IP: ip.String(), Interface: ifc.Name, Kind: "tailscale"})
			case ip.IsPrivate():
				out = append(out, Addr{IP: ip.String(), Interface: ifc.Name, Kind: "lan"})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind == "lan" && out[j].Kind != "lan" })
	return out
}

// Responder answers <name>.local on the LAN. Restart with a new name
// by calling Set again.
type Responder struct {
	Log *slog.Logger

	mu   sync.Mutex
	name string
	srv  *mdns.Conn
	err  string
}

// Set (re)starts the responder for name; "" turns it off.
func (r *Responder) Set(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".local")
	if name == r.name && (r.srv != nil || r.err != "") {
		return
	}
	if r.srv != nil {
		r.srv.Close()
		r.srv = nil
	}
	r.name, r.err = name, ""
	if name == "" {
		return
	}
	lc := net.ListenConfig{Control: reuse}
	pc, err := lc.ListenPacket(context.Background(), "udp4", mdns.DefaultAddressIPv4)
	if err != nil {
		r.err = err.Error()
		r.Log.Warn("network name not announced", "name", name+".local", "err", err)
		return
	}
	srv, err := mdns.NewServer(ipv4.NewPacketConn(pc), nil, mdns.WithLocalNames(name+".local"))
	if err != nil {
		pc.Close()
		r.err = err.Error()
		r.Log.Warn("network name not announced", "name", name+".local", "err", err)
		return
	}
	r.srv = srv
	r.Log.Info("answering on the network", "name", name+".local")
}

// State reports the name and any error.
func (r *Responder) State() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.name, r.err
}

func (r *Responder) Close() { r.Set("") }
