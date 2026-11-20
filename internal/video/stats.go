package video

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Stats is live bandwidth from the gateway's own counters (MediaMTX API
// v3), as rates over the last sampling interval. "In" is what reaches
// the gateway (from a camera or from vMix), "out" is what it sends to
// screens. One camera feed is pulled once however many screens watch.
type Stats struct {
	At       time.Time    `json:"at"`
	Running  bool         `json:"running"`
	InKbps   int          `json:"inKbps"`
	OutKbps  int          `json:"outKbps"`
	Feeds    []FeedStats  `json:"feeds"`
	Viewers  []ViewerStat `json:"viewers"`
	Senders  []SenderStat `json:"senders"` // SRT publishers (vMix program / confidence)
	Interval float64      `json:"interval"`
}

type FeedStats struct {
	Path    string `json:"path"`
	Online  bool   `json:"online"`
	Source  string `json:"source"` // rtspSource (camera), srtConn (vMix), …
	Codec   string `json:"codec,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	InKbps  int    `json:"inKbps"`
	OutKbps int    `json:"outKbps"`
	Viewers int    `json:"viewers"`
}

type ViewerStat struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // webrtc | hls
	Path    string    `json:"path"`
	Remote  string    `json:"remote"`
	Kbps    int       `json:"kbps"`
	Since   time.Time `json:"since"`
	Lost    uint64    `json:"lost,omitempty"` // RTP packets the viewer reported lost (WebRTC)
	Agent   string    `json:"agent,omitempty"`
	Connect string    `json:"connect,omitempty"` // WebRTC: local/remote candidate types
}

type SenderStat struct {
	Path     string  `json:"path"`
	Remote   string  `json:"remote"`
	Kbps     int     `json:"kbps"`
	RTTms    float64 `json:"rttMs"`
	LossRate float64 `json:"lossRate"` // percent of packets lost before retransmission
	Dropped  uint64  `json:"dropped"`  // packets that arrived too late and were dropped
}

type statsState struct {
	mu   sync.Mutex
	last Stats
	prev map[string]uint64 // counter name → value at last sample
	at   time.Time
}

const statsEvery = 2 * time.Second

// Stats returns the latest sample (empty when the gateway isn't running).
func (g *Gateway) Stats() Stats {
	g.stats.mu.Lock()
	defer g.stats.mu.Unlock()
	return g.stats.last
}

// sampleStats runs while the gateway runs.
func (g *Gateway) sampleStats(ctx context.Context) {
	t := time.NewTicker(statsEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			g.stats.mu.Lock()
			g.stats.last = Stats{At: time.Now()}
			g.stats.prev = nil
			g.stats.mu.Unlock()
			return
		case <-t.C:
			if s, err := collect(); err == nil {
				g.stats.apply(s)
			}
		}
	}
}

type raw struct {
	at      time.Time
	paths   []rawPath
	webrtc  []map[string]any
	hls     []map[string]any
	srt     []map[string]any
	readers map[string]int
}

type rawPath struct {
	m map[string]any
}

func getItems(cl *http.Client, path string) ([]map[string]any, error) {
	resp, err := cl.Get("http://" + apiAddr + path + "?itemsPerPage=500")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Items, nil
}

func collect() (*raw, error) {
	cl := &http.Client{Timeout: time.Second}
	r := &raw{at: time.Now()}
	ps, err := getItems(cl, "/v3/paths/list")
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		r.paths = append(r.paths, rawPath{p})
	}
	r.webrtc, _ = getItems(cl, "/v3/webrtc/sessions/list")
	r.hls, _ = getItems(cl, "/v3/hls/sessions/list")
	r.srt, _ = getItems(cl, "/v3/srt/conns/list")
	return r, nil
}

func num(m map[string]any, keys ...string) uint64 {
	for _, k := range keys {
		if v, ok := m[k].(float64); ok {
			return uint64(v)
		}
	}
	return 0
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func (st *statsState) apply(r *raw) {
	st.mu.Lock()
	defer st.mu.Unlock()
	secs := r.at.Sub(st.at).Seconds()
	if st.at.IsZero() || secs <= 0 {
		secs = 0
	}
	cur := map[string]uint64{}
	rate := func(key string, v uint64) int {
		cur[key] = v
		p, ok := st.prev[key]
		if !ok || secs == 0 || v < p {
			return 0
		}
		return int(float64(v-p) * 8 / 1000 / secs)
	}

	out := Stats{At: r.at, Running: true, Interval: secs}
	idx := map[string]int{}
	for _, rp := range r.paths {
		m := rp.m
		f := FeedStats{Path: str(m, "name")}
		// "available" = media is flowing; "online" only means configured.
		if v, ok := m["available"].(bool); ok {
			f.Online = v
		} else {
			f.Online, _ = m["ready"].(bool)
		}
		if src, ok := m["source"].(map[string]any); ok {
			f.Source = str(src, "type")
		}
		if rs, ok := m["readers"].([]any); ok {
			f.Viewers = len(rs)
		}
		if tr, ok := m["tracks2"].([]any); ok {
			for _, t := range tr {
				tm, _ := t.(map[string]any)
				props, _ := tm["codecProps"].(map[string]any)
				if props != nil && num(props, "width") > 0 {
					f.Codec, f.Width, f.Height = str(tm, "codec"), int(num(props, "width")), int(num(props, "height"))
					break
				}
			}
		}
		f.InKbps = rate("p.in."+f.Path, num(m, "inboundBytes", "bytesReceived"))
		f.OutKbps = rate("p.out."+f.Path, num(m, "outboundBytes", "bytesSent"))
		out.InKbps += f.InKbps
		out.OutKbps += f.OutKbps
		idx[f.Path] = len(out.Feeds)
		out.Feeds = append(out.Feeds, f)
	}
	since := func(m map[string]any) time.Time {
		t, _ := time.Parse(time.RFC3339Nano, str(m, "created"))
		return t
	}
	for _, m := range r.webrtc {
		if str(m, "state") == "read" || str(m, "state") == "" {
			id := str(m, "id")
			v := ViewerStat{ID: id, Kind: "webrtc", Path: str(m, "path"), Remote: str(m, "remoteAddr"), Since: since(m),
				Lost: num(m, "inboundRTPPacketsLost", "outboundRTPPacketsReportedLost"),
				Kbps: rate("w."+id, num(m, "outboundBytes", "bytesSent"))}
			if lc, rc := str(m, "localCandidate"), str(m, "remoteCandidate"); lc != "" || rc != "" {
				v.Connect = candType(lc) + "→" + candType(rc)
			}
			out.Viewers = append(out.Viewers, v)
		}
	}
	for _, m := range r.hls {
		id := str(m, "id")
		out.Viewers = append(out.Viewers, ViewerStat{ID: id, Kind: "hls", Path: str(m, "path"), Remote: str(m, "remoteAddr"), Since: since(m),
			Agent: str(m, "userAgent"), Kbps: rate("h."+id, num(m, "outboundBytes"))})
	}
	for _, m := range r.srt {
		if str(m, "state") != "publish" {
			continue
		}
		id := str(m, "id")
		s := SenderStat{Path: str(m, "path"), Remote: str(m, "remoteAddr"), Kbps: rate("s."+id, num(m, "bytesReceived"))}
		if v, ok := m["msRTT"].(float64); ok {
			s.RTTms = v
		}
		if v, ok := m["packetsReceivedLossRate"].(float64); ok {
			s.LossRate = v
		}
		s.Dropped = num(m, "packetsReceivedDrop")
		out.Senders = append(out.Senders, s)
	}
	sort.Slice(out.Viewers, func(i, j int) bool {
		if out.Viewers[i].Path != out.Viewers[j].Path {
			return out.Viewers[i].Path < out.Viewers[j].Path
		}
		return out.Viewers[i].Remote < out.Viewers[j].Remote
	})
	st.prev, st.at, st.last = cur, r.at, out
}

// candType turns "host/udp/192.168.1.5/8189" style strings into "host",
// "srflx", "relay"… (enough to tell a direct LAN path from a detour).
func candType(c string) string {
	for _, t := range []string{"host", "srflx", "prflx", "relay"} {
		if strings.Contains(c, t) {
			return t
		}
	}
	if c == "" {
		return "?"
	}
	return c
}
