// Package mockcam is a mock PTZOptics camera for development and tests:
// a VISCA TCP responder that integrates drive commands into simulated
// pan/tilt/zoom, plus the camera's HTTP surface (param.cgi, snapshot)
// and /__test__/ hooks for assertions and failure injection.
package mockcam

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"ptz/internal/visca"
)

//go:embed mock-snapshot.jpg
var snapshotJPEG []byte

const DeviceConf = `devname = "MockCam (PTZOptics sim)"
devtype = "V63C"
devmodel = "F16.HI"
serial = "mock00000001"
versioninfo = "SOC v6.3.45 - ARM v6.3.45THI (MOCK)"
`

type ptz struct{ pan, tilt, zoom int }

type Mock struct {
	Log *slog.Logger

	mu          sync.Mutex
	pan, tilt   int
	zoom        int
	panV, tiltV int
	zoomV       int
	presets     map[int]ptz
	focusAuto   bool
	frames      int
	absTarget   *[2]int
	wbModeByte  *int
	wbTriggers  int
	cmdLog      []string
	dropAbsAt   int
	absSeen     int
	imgKeys     []string
	img         map[string]string
	lastPost    *string
	cgiTriggers int
	conns       []net.Conn
}

func New() *Mock {
	m := &Mock{zoom: 1000, presets: map[int]ptz{}, focusAuto: true, Log: slog.Default()}
	m.resetImage()
	return m
}

func (m *Mock) resetImage() {
	pairs := [][2]string{
		// image basics: the real camera's recorded values (Nov 7, 2026)
		{"bright", "11"}, {"saturation", "4"}, {"contrast", "10"},
		{"sharpness", "3"}, {"hue", "8"}, {"flip", "0"}, {"mirror", "0"},
		{"ldc", "0"},
		// platform defaults (NOT read from the real camera)
		{"wbmode", "auto"}, {"rgain", "128"}, {"bgain", "128"},
		{"rgtuning", "0"}, {"bgtuning", "0"}, {"awbsens", "normal"},
		{"colortemp", "5000"},
		{"aemode", "auto"}, {"shutter", "1/250"}, {"iris", "F5.6"},
		{"gain", "0"}, {"expbright", "7"}, {"expcompmode", "0"},
		{"expcomp", "0"}, {"backlight", "0"}, {"gainlimit", "3"},
		{"flicker", "off"}, {"drc", "0"},
		{"afsens", "high"}, {"afzone", "center"},
		{"nr2d", "3"}, {"nr3d", "3"},
	}
	m.imgKeys = nil
	m.img = map[string]string{}
	for _, p := range pairs {
		m.imgKeys = append(m.imgKeys, p[0])
		m.img[p[0]] = p[1]
	}
}

// Serve listens on the given VISCA TCP and HTTP addresses until ctx ends.
// It returns the bound addresses (useful with ":0").
func (m *Mock) Serve(ctx context.Context, viscaAddr, httpAddr string) (string, string, error) {
	tl, err := net.Listen("tcp", viscaAddr)
	if err != nil {
		return "", "", err
	}
	hl, err := net.Listen("tcp", httpAddr)
	if err != nil {
		tl.Close()
		return "", "", err
	}
	srv := &http.Server{Handler: m}
	go srv.Serve(hl)
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			m.mu.Lock()
			m.conns = append(m.conns, c)
			m.mu.Unlock()
			go m.handle(c)
		}
	}()
	go m.integrate(ctx)
	go func() {
		<-ctx.Done()
		tl.Close()
		srv.Close()
		m.DropConnections()
	}()
	return tl.Addr().String(), hl.Addr().String(), nil
}

// DropConnections closes all VISCA client connections (reconnect tests).
func (m *Mock) DropConnections() {
	m.mu.Lock()
	cs := m.conns
	m.conns = nil
	m.mu.Unlock()
	for _, c := range cs {
		c.Close()
	}
}

func nib(v, n int) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = byte(v>>(4*(n-1-i))) & 0xF
	}
	return out
}

func (m *Mock) reply(f []byte) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case bytes.Equal(f, visca.InqPower):
		return []byte{0x90, 0x50, 0x02, 0xFF}
	case bytes.Equal(f, visca.InqPanTiltPos):
		b := append([]byte{0x90, 0x50}, nib(m.pan&0xFFFF, 4)...)
		b = append(b, nib(m.tilt&0xFFFF, 4)...)
		return append(b, 0xFF)
	case bytes.Equal(f, visca.InqZoomPos):
		return append(append([]byte{0x90, 0x50}, nib(m.zoom&0xFFFF, 4)...), 0xFF)
	case bytes.Equal(f, visca.InqFocusMode):
		v := byte(0x03)
		if m.focusAuto {
			v = 0x02
		}
		return []byte{0x90, 0x50, v, 0xFF}
	case bytes.Equal(f, visca.InqFocusPos):
		return append(append([]byte{0x90, 0x50}, nib(0x1000, 4)...), 0xFF)
	}
	return nil
}

func s16(v int) int { return int(int16(uint16(v))) }

func nv(f []byte, start, n int) int {
	v := 0
	for i := 0; i < n; i++ {
		v = v<<4 | int(f[start+i]&0xF)
	}
	return v
}

func has(f []byte, b ...byte) bool { return len(f) >= 1+len(b) && bytes.Equal(f[1:1+len(b)], b) }

func (m *Mock) apply(f []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case len(f) >= 15 && has(f, 0x01, 0x06, 0x02): // absolute move
		m.absSeen++
		if m.dropAbsAt > 0 && m.absSeen == m.dropAbsAt {
			m.Log.Info("[mock] DROPPING absolute move (failure injection)")
			m.dropAbsAt = 0
			return
		}
		m.absTarget = &[2]int{s16(nv(f, 6, 4)), s16(nv(f, 10, 4))}
		m.panV, m.tiltV = 0, 0
	case len(f) >= 6 && has(f, 0x01, 0x04, 0x10): // one-push WB trigger
		if f[4] == 0x05 {
			m.wbTriggers++
		}
	case len(f) >= 6 && has(f, 0x01, 0x04, 0x35): // WB mode
		v := int(f[4])
		m.wbModeByte = &v
	case len(f) >= 9 && has(f, 0x01, 0x06, 0x01): // pan/tilt drive
		vv, ww, x, y := int(f[4]), int(f[5]), f[6], f[7]
		switch x {
		case 3:
			m.panV = 0
		case 1:
			m.panV = -vv
		default:
			m.panV = vv
		}
		switch y {
		case 3:
			m.tiltV = 0
		case 1:
			m.tiltV = ww
		default:
			m.tiltV = -ww
		}
	case len(f) >= 6 && has(f, 0x01, 0x04, 0x07): // zoom
		b := f[4]
		switch {
		case b == 0:
			m.zoomV = 0
		case b&0xF0 == 0x20:
			m.zoomV = int(b&0xF) + 1
		case b&0xF0 == 0x30:
			m.zoomV = -(int(b&0xF) + 1)
		}
	case len(f) >= 7 && has(f, 0x01, 0x04, 0x3F): // preset
		mode, slot := f[4], int(f[5])
		switch mode {
		case 1:
			m.presets[slot] = ptz{m.pan, m.tilt, m.zoom}
		case 2:
			if p, ok := m.presets[slot]; ok {
				m.pan, m.tilt, m.zoom = p.pan, p.tilt, p.zoom
			}
		case 0:
			delete(m.presets, slot)
		}
	case len(f) >= 6 && has(f, 0x01, 0x04, 0x38):
		m.focusAuto = f[4] == 0x02
	case has(f, 0x01, 0x06, 0x04): // home
		m.pan, m.tilt = 0, 0
	}
}

func (m *Mock) handle(c net.Conn) {
	defer c.Close()
	buf := []byte{}
	tmp := make([]byte, 4096)
	for {
		n, err := c.Read(tmp)
		if n > 0 {
			var frames [][]byte
			frames, buf = visca.SplitFrames(append(buf, tmp[:n]...))
			buf = append([]byte(nil), buf...)
			for _, f := range frames {
				if len(f) < 2 {
					continue
				}
				m.mu.Lock()
				m.frames++
				if f[1] == 0x01 {
					m.cmdLog = append(m.cmdLog, hex.EncodeToString(f))
				}
				m.mu.Unlock()
				if f[1] == 0x09 {
					if r := m.reply(f); r != nil {
						c.Write(r)
					}
				} else {
					m.apply(f)
					c.Write([]byte{0x90, 0x41, 0xFF, 0x90, 0x51, 0xFF})
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

func (m *Mock) integrate(ctx context.Context) {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.mu.Lock()
		if m.absTarget != nil {
			dp := clamp(m.absTarget[0]-m.pan, -300, 300)
			dt := clamp(m.absTarget[1]-m.tilt, -300, 300)
			m.pan += dp
			m.tilt += dt
			if dp == 0 && dt == 0 {
				m.absTarget = nil
			}
		} else {
			m.pan = clamp(m.pan+m.panV*2, visca.PanMin, visca.PanMax)
			m.tilt = clamp(m.tilt+m.tiltV*2, visca.TiltMin, visca.TiltMax)
		}
		m.zoom = clamp(m.zoom+m.zoomV*30, 0, 16384)
		m.mu.Unlock()
	}
}

// State is what /__test__/visca returns.
type State struct {
	Log             []string `json:"log"`
	Pan             int      `json:"pan"`
	Tilt            int      `json:"tilt"`
	Zoom            int      `json:"zoom"`
	WBModeByte      *int     `json:"wb_mode_byte"`
	WBVISCATriggers int      `json:"wb_visca_triggers"`
	AbsSeen         int      `json:"abs_seen"`
	Frames          int      `json:"frames"`
}

func (m *Mock) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return State{Log: append([]string(nil), m.cmdLog...), Pan: m.pan, Tilt: m.tilt,
		Zoom: m.zoom, WBModeByte: m.wbModeByte, WBVISCATriggers: m.wbTriggers,
		AbsSeen: m.absSeen, Frames: m.frames}
}

// Reset clears logs and motion; optional position.
func (m *Mock) Reset(pan, tilt, zoom *int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pan != nil {
		m.pan = *pan
	}
	if tilt != nil {
		m.tilt = *tilt
	}
	if zoom != nil {
		m.zoom = *zoom
	}
	m.panV, m.tiltV, m.zoomV = 0, 0, 0
	m.absTarget = nil
	m.cmdLog = nil
	m.wbModeByte = nil
	m.wbTriggers = 0
	m.absSeen = 0
	m.dropAbsAt = 0
	m.frames = 0
}

// DropAbs makes the nth absolute move from now be ACKed but ignored.
func (m *Mock) DropAbs(n int) {
	m.mu.Lock()
	m.absSeen = 0
	m.dropAbsAt = n
	m.mu.Unlock()
}

func qint(q url.Values, k string) *int {
	if v, err := strconv.Atoi(q.Get(k)); err == nil {
		return &v
	}
	return nil
}

func (m *Mock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case strings.HasPrefix(r.URL.Path, "/__test__/visca"):
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(m.State())
	case strings.HasPrefix(r.URL.Path, "/__test__/reset"):
		m.Reset(qint(q, "pan"), qint(q, "tilt"), qint(q, "zoom"))
		fmt.Fprint(w, "OK")
	case strings.HasPrefix(r.URL.Path, "/__test__/dropabs"):
		n, _ := strconv.Atoi(q.Get("n"))
		m.DropAbs(n)
		fmt.Fprint(w, "OK")
	case strings.HasPrefix(r.URL.Path, "/__test__/image"):
		m.mu.Lock()
		conf := map[string]string{}
		for k, v := range m.img {
			conf[k] = v
		}
		body := map[string]any{"conf": conf, "last_post": m.lastPost, "wb_triggers": m.cgiTriggers}
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	case r.URL.Path == "/cgi-bin/param.cgi":
		m.cgi(w, r.URL.RawQuery)
	case r.URL.Path == "/snapshot.jpg":
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(snapshotJPEG)
	default:
		http.NotFound(w, r)
	}
}

func (m *Mock) cgi(w http.ResponseWriter, raw string) {
	tokens := []string{}
	if raw != "" {
		tokens = strings.Split(raw, "&")
	}
	cmd := ""
	if len(tokens) > 0 {
		cmd = tokens[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch cmd {
	case "get_image_conf":
		var b strings.Builder
		for _, k := range m.imgKeys {
			fmt.Fprintf(&b, "%s = \"%s\"\n", k, m.img[k])
		}
		fmt.Fprint(w, b.String())
	case "post_image_conf":
		for _, t := range tokens[1:] {
			k, v, ok := strings.Cut(t, "=")
			if !ok {
				continue
			}
			k, _ = url.PathUnescape(k)
			v, _ = url.PathUnescape(v)
			if _, exists := m.img[k]; !exists {
				m.imgKeys = append(m.imgKeys, k)
			}
			m.img[k] = v
		}
		rq := raw
		m.lastPost = &rq
		fmt.Fprint(w, "OK")
	case "post_image_value":
		if slicesContains(tokens, "wbmode") && slicesContains(tokens, "trigger") {
			m.cgiTriggers++
		}
		fmt.Fprint(w, "OK")
	default:
		fmt.Fprint(w, DeviceConf)
	}
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
