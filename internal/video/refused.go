package video

import (
	"net"
	"regexp"
	"strings"
	"time"
)

// Refused senders. A sender with a wrong or old key, or one sending to
// a path the gateway doesn't have, is turned away and the feed just
// waits forever. MediaMTX logs each refusal on stdout (which the app
// reads anyway), e.g.
//
//	INF [SRT] [conn 192.168.1.50:61000] closed: failed to authenticate: authentication failed
//	INF [RTSP] [conn 192.168.1.50:55168] closed: path 'progam' is not configured
//
// A key refusal names no path, so it is matched to a feed by the
// sender's address (where that address last published) or, when the
// address is new, shown on every push feed that is waiting for a sender.

// Refusal is one sender the gateway turned away.
type Refusal struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`         // key | path | streamid
	Path   string    `json:"path,omitempty"` // when the gateway named it
	Remote string    `json:"remote,omitempty"`
	Proto  string    `json:"proto"` // SRT | RTSP | RTMP
}

const (
	refusedKeep   = 32
	RefusedWindow = 15 * time.Minute // how long a refusal is shown
)

type senderSeen struct {
	path string
	at   time.Time
}

var (
	refusedRe    = regexp.MustCompile(`\[(SRT|RTSP|RTMP)\] \[conn (.+?)\] closed: (.+)$`)
	notConfRe    = regexp.MustCompile(`path '([^']+)' is not configured`)
	logTimeRe    = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) `)
	logTimeFmt   = "2006/01/02 15:04:05"
	nowForRefuse = time.Now
)

// parseRefusal reads a gateway log line; ok when it is a refused sender.
func parseRefusal(line string) (Refusal, bool) {
	m := refusedRe.FindStringSubmatch(line)
	if m == nil {
		return Refusal{}, false
	}
	r := Refusal{Proto: m[1], Remote: m[2]}
	if h, _, err := net.SplitHostPort(m[2]); err == nil {
		r.Remote = h
	}
	msg := m[3]
	switch {
	case strings.Contains(msg, "failed to authenticate") || strings.Contains(msg, "authentication failed"):
		r.Reason = "key"
	case notConfRe.MatchString(msg):
		r.Reason, r.Path = "path", notConfRe.FindStringSubmatch(msg)[1]
		if strings.HasPrefix(r.Path, "cam-") {
			return Refusal{}, false // a reader asking for a camera that's off, not a sender
		}
	case strings.Contains(msg, "invalid stream ID"):
		r.Reason = "streamid"
	default:
		return Refusal{}, false
	}
	r.At = nowForRefuse()
	if t := logTimeRe.FindStringSubmatch(line); t != nil {
		if at, err := time.ParseInLocation(logTimeFmt, t[1], time.Local); err == nil {
			r.At = at
		}
	}
	return r, true
}

// NoteLogLine records a refused sender from one gateway log line (the
// supervisor passes every line of MediaMTX's output here).
func (g *Gateway) NoteLogLine(line string) {
	r, ok := parseRefusal(line)
	if !ok {
		return
	}
	g.mu.Lock()
	g.refused = append(g.refused, r)
	if len(g.refused) > refusedKeep {
		g.refused = g.refused[len(g.refused)-refusedKeep:]
	}
	g.mu.Unlock()
	if r.Reason == "path" {
		g.verifyPath(r.Path) // a path we wrote but the gateway hasn't loaded: repair now
	}
}

// noteSenders remembers which address publishes which path.
func (g *Gateway) noteSenders(ss []SenderStat, at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.senderIPs == nil {
		g.senderIPs = map[string]senderSeen{}
	}
	for _, s := range ss {
		ip := s.Remote
		if h, _, err := net.SplitHostPort(ip); err == nil {
			ip = h
		}
		if ip != "" && s.Path != "" {
			g.senderIPs[ip] = senderSeen{path: s.Path, at: at}
		}
	}
	if len(g.senderIPs) > 200 {
		for ip, s := range g.senderIPs {
			if at.Sub(s.at) > 24*time.Hour {
				delete(g.senderIPs, ip)
			}
		}
	}
}

// Refused returns, per push feed, the latest refused sender meant for it
// in the last RefusedWindow. push lists the push feed ids; online says
// which of them are receiving now (a refusal not naming its feed is
// shown only on feeds still waiting for a sender).
func (g *Gateway) Refused(push []string, online map[string]bool) map[string]Refusal {
	g.mu.Lock()
	rs := append([]Refusal(nil), g.refused...)
	seen := map[string]string{}
	for ip, s := range g.senderIPs {
		seen[ip] = s.path
	}
	g.mu.Unlock()
	isPush := map[string]bool{}
	for _, id := range push {
		isPush[id] = true
	}
	now := nowForRefuse()
	out := map[string]Refusal{}
	put := func(id string, r Refusal) {
		if cur, ok := out[id]; !ok || !r.At.Before(cur.At) {
			out[id] = r
		}
	}
	waiting := func(r Refusal) {
		for _, id := range push {
			if !online[id] {
				put(id, r)
			}
		}
	}
	for _, r := range rs {
		if now.Sub(r.At) > RefusedWindow {
			continue
		}
		switch {
		case r.Path != "" && isPush[r.Path]:
			put(r.Path, r)
		case r.Path != "":
			waiting(r) // a path that isn't a feed here (an old or mistyped Stream ID)
		case isPush[seen[r.Remote]]:
			put(seen[r.Remote], r)
		default:
			waiting(r)
		}
	}
	return out
}
