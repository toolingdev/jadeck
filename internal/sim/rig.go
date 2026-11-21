package sim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ptz/internal/mockcam"
)

// Config is the simulated rig.
type Config struct {
	Cameras   int
	Host      string // where the gear listens, e.g. 127.0.0.1
	ViscaBase int    // camera i: ViscaBase+i
	HTTPBase  int
	RTSPPort  int    // one RTSP server for every camera: /cam1, /cam1-sd, …
	Switcher  string // vMix Web Controller (HTTP)
	VMixTCP   string // vMix TCP API ("" = off)
	Slides    string
	Mixer     string // WING OSC (UDP) ("" = no WING)
	WingTCP   string // WING native protocol (TCP) and discovery (UDP)
	Program   string // where to publish the program picture ("" = off)
	FPS       int
	FFmpeg    string
	MediaMTX  string
	Log       *slog.Logger
}

var names = []string{"Wide", "Lectern", "Band", "Singers", "Audience", "Keys", "Drums", "Screen"}

// Where each camera starts: pan, tilt (VISCA units), zoom.
var starts = [][3]int{{0, 63, 0}, {101, 20, 1470}, {-567, -13, 495}, {548, 0, 665}, {0, -315, 0}, {-416, -31, 1725}, {-680, -63, 1700}, {0, 185, 500}}

type camera struct {
	name  string
	n     int
	mock  *mockcam.Mock
	mu    sync.Mutex
	frame *image.RGBA
	jpg   []byte
	enc   *Encoder
}

func (c *camera) Frame() *image.RGBA {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frame
}

func (c *camera) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.jpg
}

// Rig is everything running.
type Rig struct {
	cfg     Config
	scene   *Scene
	slides  *Slides
	sw      *Switcher
	wing    *Wing
	cams    []*camera
	program *Encoder
}

const W, H = 1280, 720

// Start brings up the whole rig; it stops with ctx.
func Start(ctx context.Context, cfg Config) (*Rig, error) {
	if cfg.FPS == 0 {
		cfg.FPS = 15
	}
	r := &Rig{cfg: cfg, scene: NewScene(), slides: NewSlides(), wing: NewWing()}
	r.wing.Log = cfg.Log
	r.scene.SetSlide(r.slides.Image())
	r.slides.OnChange = func(img *image.RGBA) { r.scene.SetSlide(img) }

	if err := r.startRTSP(ctx); err != nil {
		return nil, err
	}

	var inputs []Input
	for i := 0; i < cfg.Cameras; i++ {
		m := mockcam.New()
		m.Log = cfg.Log
		s := starts[i%len(starts)]
		m.Reset(&s[0], &s[1], &s[2])
		name := "Camera " + strconv.Itoa(i+1)
		if i < len(names) {
			name = names[i]
		}
		c := &camera{name: name, n: i + 1, mock: m, frame: image.NewRGBA(image.Rect(0, 0, W, H))}
		m.Snapshot = c.snapshot
		va, ha, err := m.Serve(ctx, net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.ViscaBase+i)), net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.HTTPBase+i)))
		if err != nil {
			return nil, fmt.Errorf("camera %d: %w", i+1, err)
		}
		rtsp := fmt.Sprintf("rtsp://127.0.0.1:%d/cam%d", cfg.RTSPPort, i+1)
		c.enc = &Encoder{FFmpeg: cfg.FFmpeg, W: W, H: H, FPS: cfg.FPS, Log: cfg.Log, Name: name,
			Outputs: []Output{{URL: rtsp, Kbps: 2500}, {URL: rtsp + "-sd", Scale: "640:360", Kbps: 700}}}
		go c.enc.Run(ctx)
		r.cams = append(r.cams, c)
		inputs = append(inputs, Input{Title: name, Type: "Capture", Frame: c.Frame})
		cfg.Log.Info("camera", "name", name, "visca", va, "http", ha, "video", rtsp)
	}
	inputs = append(inputs, Input{Title: "Slides", Type: "PowerPoint", Frame: r.slides.Image})
	r.sw = NewSwitcher(inputs)
	r.sw.Log = cfg.Log
	r.sw.AddServiceInputs()

	if err := r.sw.Serve(cfg.Switcher); err != nil {
		return nil, err
	}
	if err := r.slides.ServeProclaim(cfg.Slides, cfg.Log); err != nil {
		return nil, err
	}
	if cfg.VMixTCP != "" {
		if err := r.sw.ServeTCP(cfg.VMixTCP); err != nil {
			return nil, err
		}
	}
	if cfg.Mixer != "" {
		host, op, _ := net.SplitHostPort(cfg.Mixer)
		oscPort, _ := strconv.Atoi(op)
		nativePort := 2222
		if cfg.WingTCP != "" {
			_, np, _ := net.SplitHostPort(cfg.WingTCP)
			nativePort, _ = strconv.Atoi(np)
		}
		if err := r.wing.Serve(host, oscPort, nativePort); err != nil {
			return nil, err
		}
		o, n := r.wing.Ports()
		cfg.Log.Info("mixer (Behringer WING)", "host", host, "osc", o, "native", n, "discovery", n)
	} else {
		r.wing = nil
	}
	cfg.Log.Info("switcher (vMix API)", "http", cfg.Switcher, "tcp", cfg.VMixTCP)
	cfg.Log.Info("slides (Proclaim network control)", "at", cfg.Slides)

	for _, c := range r.cams {
		go r.cameraLoop(ctx, c)
	}
	if cfg.Program != "" {
		r.program = &Encoder{FFmpeg: cfg.FFmpeg, W: W, H: H, FPS: cfg.FPS, Log: cfg.Log, Name: "program",
			Outputs: []Output{{URL: cfg.Program, Kbps: 3000}}}
		go r.program.Run(ctx)
		go r.programLoop(ctx)
		cfg.Log.Info("program feed", "to", cfg.Program)
	}
	return r, nil
}

func (r *Rig) cameraLoop(ctx context.Context, c *camera) {
	t := time.NewTicker(time.Second / time.Duration(r.cfg.FPS))
	defer t.Stop()
	last := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st := c.mock.State()
		var tally color.Color
		switch r.sw.Tally(c.n) {
		case 1:
			tally = tallyRed
		case 2:
			tally = tallyGreen
		}
		img := image.NewRGBA(image.Rect(0, 0, W, H))
		r.scene.Render(img, View{Pan: st.Pan, Tilt: st.Tilt, Zoom: st.Zoom}, c.name, tally)
		c.mu.Lock()
		c.frame = img
		c.mu.Unlock()
		c.enc.Frame(img)
		if time.Since(last) > time.Second {
			last = time.Now()
			var b bytes.Buffer
			jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
			c.mu.Lock()
			c.jpg = b.Bytes()
			c.mu.Unlock()
		}
	}
}

func (r *Rig) programLoop(ctx context.Context) {
	t := time.NewTicker(time.Second / time.Duration(r.cfg.FPS))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		img := image.NewRGBA(image.Rect(0, 0, W, H))
		r.sw.Program(img)
		r.program.Frame(img)
	}
}

// startRTSP runs a private MediaMTX that the cameras publish into and
// jadeck reads from, like each camera's own RTSP server.
func (r *Rig) startRTSP(ctx context.Context) error {
	if r.cfg.MediaMTX == "" {
		return fmt.Errorf("MediaMTX not found: pass --mediamtx (jadeck unpacks one into <data>/video/bin)")
	}
	if _, err := exec.LookPath(r.cfg.FFmpeg); err != nil {
		return fmt.Errorf("ffmpeg not found (%s): install it (brew install ffmpeg) or pass --ffmpeg", r.cfg.FFmpeg)
	}
	dir, err := os.MkdirTemp("", "ptzsim-")
	if err != nil {
		return err
	}
	conf := filepath.Join(dir, "mediamtx.yml")
	os.WriteFile(conf, []byte(fmt.Sprintf(`logLevel: warn
api: no
metrics: no
pprof: no
playback: no
rtmp: no
moq: no
hls: no
webrtc: no
srt: no
rtsp: yes
rtspAddress: :%d
rtspTransports: [tcp]
paths:
  all_others:
`, r.cfg.RTSPPort)), 0o644)
	cmd := exec.CommandContext(ctx, r.cfg.MediaMTX, conf)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("camera video server: %w", err)
	}
	go func() { cmd.Wait(); os.RemoveAll(dir) }()
	r.cfg.Log.Info("camera video server (RTSP)", "port", r.cfg.RTSPPort)
	return nil
}

// ------------------------------------------------------------- setup

// Setup adds the simulated gear to a running jadeck (its API base URL):
// cameras, then vMix / Proclaim / WING connections. Existing ones are
// left alone.
func (r *Rig) Setup(ctx context.Context, base string) error {
	post := func(path string, body any) (int, string) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		var out bytes.Buffer
		out.ReadFrom(resp.Body)
		return resp.StatusCode, out.String()
	}
	host := r.cfg.Host
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	for i, c := range r.cams {
		code, msg := post("/api/cameras", map[string]any{"name": c.name, "type": "ptzoptics", "host": host,
			"viscaPort": r.cfg.ViscaBase + i, "httpPort": r.cfg.HTTPBase + i, "rtspPort": r.cfg.RTSPPort,
			"streamHd": fmt.Sprintf("/cam%d", i+1), "streamSd": fmt.Sprintf("/cam%d-sd", i+1)})
		r.cfg.Log.Info("setup: camera", "name", c.name, "status", code, "detail", short(msg))
	}
	if r.cfg.Program != "" {
		code, _ := post("/api/feeds", map[string]any{"name": "Program", "source": "push", "program": true})
		r.cfg.Log.Info("setup: program feed", "status", code)
		r.useProgramKey(ctx, base)
	}
	// which camera is on which vMix input (camera n = input n)
	camInputs := map[string]int{}
	if req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/cameras", nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			var list []struct{ ID, Name string }
			json.NewDecoder(resp.Body).Decode(&list)
			resp.Body.Close()
			for i, c := range r.cams {
				for _, l := range list {
					if l.Name == c.name {
						camInputs[l.ID] = i + 1
					}
				}
			}
		}
	}
	port := func(a string) int {
		_, p, _ := net.SplitHostPort(a)
		n, _ := strconv.Atoi(p)
		return n
	}
	vcfg := map[string]any{"host": host, "port": port(r.cfg.Switcher), "link": "http"}
	if r.cfg.VMixTCP != "" {
		vcfg["tcpPort"], vcfg["link"] = port(r.cfg.VMixTCP), "tcp"
	}
	conns := []map[string]any{
		{"addon": "vmix", "type": "vmix", "name": "vMix (simulated)", "config": vcfg, "cameraInputs": camInputs},
		{"addon": "proclaim", "type": "proclaim", "name": "Proclaim (simulated)", "config": map[string]any{"host": host}},
	}
	if r.wing != nil {
		o, n := r.wing.Ports()
		conns = append(conns, map[string]any{"addon": "behringer-wing", "type": "osc", "name": "WING (simulated)",
			"config": map[string]any{"preset": "wing", "host": host, "port": o, "nativePort": n},
			"favorites": []map[string]any{{"label": "Speaker", "channel": 1}, {"label": "Lectern", "channel": 2}, {"label": "Lead vox", "channel": 3},
				{"label": "Keys", "channel": 6}, {"label": "Choir", "channel": 12}, {"label": "Video", "channel": 13}}})
	}
	for _, c := range conns {
		code, msg := post("/api/connections", c)
		r.cfg.Log.Info("setup: connection", "name", c["name"], "status", code, "detail", short(msg))
	}
	r.dashboard(ctx, base, post)
	post("/api/setup", map[string]any{"done": true})
	return nil
}

// useProgramKey reads the program feed's publish key from jadeck (this
// PC is on its trusted network) and has the program encoder send with
// it, unless -program already carries credentials.
func (r *Rig) useProgramKey(ctx context.Context, base string) bool {
	if r.program == nil || strings.Contains(r.cfg.Program, "@") || strings.Count(r.cfg.Program, ":") > 4 {
		return true
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/feeds", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		Feeds []struct {
			ID   string `json:"id"`
			Push *struct {
				Key string `json:"key"`
			} `json:"push"`
		} `json:"feeds"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	for _, f := range body.Feeds {
		if f.ID == "program" && f.Push != nil && f.Push.Key != "" {
			r.program.SetURL(0, WithPublishKey(r.cfg.Program, "program", f.Push.Key))
			r.cfg.Log.Info("program feed: sending with its publish key")
			return true
		}
	}
	r.cfg.Log.Warn("program feed: no publish key found; jadeck will refuse the program picture")
	return false
}

// FollowProgramKey reads the program feed's publish key from a jadeck
// that already has the gear (no -setup), retrying until jadeck answers.
func (r *Rig) FollowProgramKey(ctx context.Context, base string) {
	for !r.useProgramKey(ctx, base) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// WithPublishKey adds a push feed's publish key to a sender address:
// rtsp://host:8554/<id> → rtsp://<id>:<key>@host:8554/<id>, and an SRT
// stream id publish:<id> → publish:<id>:<id>:<key>.
func WithPublishKey(addr, id, key string) string {
	if rest, ok := strings.CutPrefix(addr, "rtsp://"); ok {
		return "rtsp://" + id + ":" + key + "@" + rest
	}
	return strings.Replace(addr, "streamid=publish:"+id, "streamid=publish:"+id+":"+id+":"+key, 1)
}

func short(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// dashboard adds "Simulator": every camera, the program, switching,
// slides, PTZ, presets and the mixer, on one desktop screen.
func (r *Rig) dashboard(ctx context.Context, base string, post func(string, any) (int, string)) {
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/cameras", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var cams []struct{ ID, Name string }
	json.NewDecoder(resp.Body).Decode(&cams)
	var widgets []map[string]any
	var layout []map[string]int
	var ids []string
	add := func(id, typ string, set map[string]any, x, y, w, h int) {
		widgets = append(widgets, map[string]any{"id": id, "type": typ, "settings": set})
		layout = append(layout, map[string]int{"x": x, "y": y, "w": w, "h": h})
		ids = append(ids, id)
	}
	n := 0
	for _, c := range cams {
		for _, mine := range r.cams {
			if mine.name == c.Name && n < 4 {
				add(fmt.Sprintf("cam%d", n+1), "video", map[string]any{"feed": "cam:" + c.ID, "tally": true, "quality": "sd"}, n*3, 0, 3, 4)
				n++
			}
		}
	}
	add("program", "video", map[string]any{"feed": "program", "title": "Program"}, 0, 4, 6, 7)
	add("inputs", "switcher.inputs", map[string]any{}, 6, 4, 6, 3)
	add("cut", "switcher.cutfade", map[string]any{"fadeMs": 1000}, 6, 7, 3, 3)
	add("slides", "slides.prevnext", map[string]any{}, 9, 7, 3, 3)
	add("move", "camera.move", map[string]any{}, 0, 11, 4, 10)
	add("presets", "camera.presets", map[string]any{}, 4, 11, 4, 8)
	add("mixer", "audio.favorites", map[string]any{}, 8, 11, 4, 8)
	lay := make([]map[string]any, len(layout))
	for i, l := range layout {
		lay[i] = map[string]any{"i": ids[i], "x": l["x"], "y": l["y"], "w": l["w"], "h": l["h"]}
	}
	code, msg := post("/api/dashboards", map[string]any{"name": "Simulator", "kind": "desktop", "widgets": widgets, "layout": lay})
	r.cfg.Log.Info("setup: dashboard", "status", code, "detail", short(msg))
}
