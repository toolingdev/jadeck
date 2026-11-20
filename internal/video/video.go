// Package video runs the stream gateway (MediaMTX) for the app: it
// writes the gateway's config from Settings, starts and supervises the
// process, and can fetch the pinned MediaMTX release (sha256-checked)
// when it isn't installed yet. Browsers cannot play a camera's RTSP
// feed; the gateway pulls it once and re-serves WebRTC (sub-second)
// and HLS (Safari fallback).
package video

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ptz/internal/settings"
)

// Pinned MediaMTX release (latest stable, published 2026-11-15).
const Version = "v1.21.4"

var pinned = map[string]string{
	"darwin_amd64":  "c8e2423944731bfa11f29029786961492639ffbacd30c0013aa0556a37d112b8",
	"darwin_arm64":  "20dfa23c1db149b935c2f23439ffbf0820a0ac1d6979a6eb9b21f74fc8376647",
	"linux_amd64":   "be0c05d9c8af8338116977e7f1c3fcb409fbf882075e5ab075cf8baad76a59d2",
	"linux_arm64":   "47784081da54c0608ea4dc359f5cfb19880209434769c532b3ed3a67ea99c29d",
	"linux_armv7":   "48a067993e28e409478463dc71d2ee8a82511acebc4956f685ecfeda2b0bd9ae",
	"windows_amd64": "835645b2700ffce4d2b6cb04bfa0aadce95335da6dfc0d7c2114e6da585734f8",
}

const (
	PortRTSP   = 8554
	PortWebRTC = 8889
	PortHLS    = 8888
	PortSRT    = 8890
	apiAddr    = "127.0.0.1:9997"
)

func platform() string {
	arch := runtime.GOARCH
	if arch == "arm" {
		arch = "armv7"
	}
	return runtime.GOOS + "_" + arch
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "mediamtx.exe"
	}
	return "mediamtx"
}

// PathHD / PathSD are a camera's gateway paths.
func PathHD(id string) string { return "cam-" + id }
func PathSD(id string) string { return "cam-" + id + "-sd" }

type PathState struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Readers int    `json:"readers"`
}

type Status struct {
	Managed   bool        `json:"managed"`
	State     string      `json:"state"` // off | missing | installing | starting | running | failed
	Version   string      `json:"version"`
	Error     string      `json:"error,omitempty"`
	Installed string      `json:"installed,omitempty"` // binary path
	CanFetch  bool        `json:"canFetch"`            // a pinned build exists for this OS
	Bundled   bool        `json:"bundled"`             // shipped inside this ptz build
	Paths     []PathState `json:"paths"`
	Log       []string    `json:"log"`
}

type Gateway struct {
	Dir    string // <data>/video: config, downloaded binary, pid
	ExeDir string // beside the ptz binary (a bundled mediamtx wins)
	Log    *slog.Logger

	mu         sync.Mutex
	want       bool
	state      string
	err        string
	installing bool
	logs       []string
	kick       chan struct{}
	cmd        *exec.Cmd
	stats      statsState
	repeats    map[string]*repeat
}

type repeat struct {
	at time.Time
	n  int
}

var tsPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)

// quiet reports whether line repeats one already logged in the last
// minute (and counts it). Once the minute is up the next repeat is
// logged with how many were held back.
func (g *Gateway) quiet(line string) bool {
	key := tsPrefix.ReplaceAllString(line, "")
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.repeats == nil {
		g.repeats = map[string]*repeat{}
	}
	r := g.repeats[key]
	now := time.Now()
	if r != nil && now.Sub(r.at) < time.Minute {
		r.n++
		return true
	}
	if r != nil && r.n > 0 {
		g.Log.Info("gateway: earlier message repeated", "times", r.n, "line", key)
	}
	g.repeats[key] = &repeat{at: now}
	if len(g.repeats) > 500 {
		g.repeats = map[string]*repeat{key: g.repeats[key]}
	}
	return false
}

func (g *Gateway) init() {
	if g.kick == nil {
		g.kick = make(chan struct{}, 1)
	}
}

func (g *Gateway) poke() {
	select {
	case g.kick <- struct{}{}:
	default:
	}
}

// Bundled reports whether this build carries MediaMTX inside.
func Bundled() bool { return len(bundled) > 0 }

// unpack writes the bundled MediaMTX to <data>/video/bin when it is
// missing or a different version (after an update).
func (g *Gateway) unpack() {
	if !Bundled() {
		return
	}
	dst := filepath.Join(g.Dir, "bin", exeName())
	want := sha256.Sum256(bundled)
	if cur, err := os.ReadFile(dst); err == nil && sha256.Sum256(cur) == want {
		return
	}
	if err := writeExe(dst, bundled); err != nil {
		g.Log.Error("bundled stream gateway not unpacked", "err", err)
		return
	}
	os.WriteFile(filepath.Join(g.Dir, "bin", "MEDIAMTX-LICENSE.txt"), bundledLicense, 0o644)
	g.Log.Info("stream gateway unpacked", "version", Version, "path", dst)
}

// Binary finds mediamtx: beside ptz, in the data folder, or on PATH.
func (g *Gateway) Binary() string {
	for _, p := range []string{
		filepath.Join(g.ExeDir, exeName()),
		filepath.Join(g.ExeDir, "mediamtx", exeName()),
		filepath.Join(g.Dir, "bin", exeName()),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath(exeName()); err == nil {
		return p
	}
	return ""
}

func (g *Gateway) configPath() string { return filepath.Join(g.Dir, "mediamtx.yml") }

func q(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Config renders the gateway config for s.
func Config(s settings.Settings) string {
	var b strings.Builder
	b.WriteString("# Written by the PTZ controller from Settings → Video. Changes here are overwritten.\n")
	b.WriteString("logLevel: info\nlogDestinations: [stdout]\n")
	fmt.Fprintf(&b, "api: yes\napiAddress: %s\n", apiAddr)
	fmt.Fprintf(&b, "rtsp: yes\nrtspAddress: :%d\nrtspTransports: [tcp]\n", PortRTSP)
	b.WriteString("rtmp: no\nmoq: no\n")
	fmt.Fprintf(&b, "webrtc: yes\nwebrtcAddress: :%d\nwebrtcAllowOrigins: ['*']\nwebrtcICEServers2: []\n", PortWebRTC)
	fmt.Fprintf(&b, "hls: yes\nhlsAddress: :%d\nhlsAllowOrigins: ['*']\nhlsVariant: lowLatency\n", PortHLS)
	srt := false
	for _, f := range s.Video.Feeds {
		srt = srt || f.Source == "push" || strings.HasPrefix(f.URL, "srt://")
	}
	if srt {
		fmt.Fprintf(&b, "srt: yes\nsrtAddress: :%d\n", PortSRT)
	} else {
		b.WriteString("srt: no\n")
	}
	b.WriteString("paths:\n")
	for _, c := range s.Cameras {
		if !c.Video {
			continue
		}
		for _, p := range []struct{ name, path string }{{PathHD(c.ID), c.StreamHD}, {PathSD(c.ID), c.StreamSD}} {
			fmt.Fprintf(&b, "  %s:\n    source: %s\n    rtspTransport: tcp\n    sourceOnDemand: yes\n", p.name, q(c.RTSP(p.path)))
		}
	}
	for _, f := range s.Video.Feeds {
		switch f.Source {
		case "push":
			fmt.Fprintf(&b, "  %s:\n    source: publisher\n", f.ID)
		case "pull":
			if f.URL == "" {
				continue
			}
			fmt.Fprintf(&b, "  %s:\n    source: %s\n    sourceOnDemand: yes\n", f.ID, q(f.URL))
			if strings.HasPrefix(f.URL, "rtsp") {
				b.WriteString("    rtspTransport: tcp\n")
			}
		}
	}
	return b.String()
}

// Apply writes the config and starts/stops the gateway to match s.
// A running MediaMTX reloads its config file on its own.
func (g *Gateway) Apply(s settings.Settings) error {
	g.mu.Lock()
	g.init()
	g.want = s.Video.Managed
	g.mu.Unlock()
	if err := os.MkdirAll(g.Dir, 0o755); err != nil {
		return err
	}
	tmp := g.configPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(Config(s)), 0o600); err != nil { // may hold camera logins
		return err
	}
	if err := os.Rename(tmp, g.configPath()); err != nil {
		return err
	}
	g.poke()
	return nil
}

func (g *Gateway) setState(st, err string) {
	g.mu.Lock()
	g.state, g.err = st, err
	g.mu.Unlock()
}

func (g *Gateway) addLog(line string) {
	// Into the app's log too: problems as warnings, the rest at debug.
	// The same message repeating (a camera that's off, retried every few
	// seconds) is written once a minute with a count, not every time.
	if g.Log != nil && g.quiet(line) {
		g.mu.Lock()
		g.logs = append(g.logs, line)
		if len(g.logs) > 60 {
			g.logs = g.logs[len(g.logs)-60:]
		}
		g.mu.Unlock()
		return
	}
	if g.Log != nil {
		switch {
		case strings.Contains(line, " ERR ") || strings.Contains(line, "[ERR]"):
			g.Log.Warn("gateway", "line", line)
		case strings.Contains(line, " WAR ") || strings.Contains(line, "[WAR]"):
			g.Log.Warn("gateway", "line", line)
		default:
			g.Log.Debug("gateway", "line", line)
		}
	}
	g.mu.Lock()
	g.logs = append(g.logs, line)
	if len(g.logs) > 60 {
		g.logs = g.logs[len(g.logs)-60:]
	}
	g.mu.Unlock()
}

type lineWriter struct {
	g   *Gateway
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.g.addLog(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// killOrphan stops a gateway left running by a previous crash of this
// app (it would hold the ports). Ours is the one answering our API port.
func (g *Gateway) killOrphan() {
	b, err := os.ReadFile(filepath.Join(g.Dir, "mediamtx.pid"))
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	cl := http.Client{Timeout: 700 * time.Millisecond}
	if resp, err := cl.Get("http://" + apiAddr + "/v3/paths/list"); err == nil {
		resp.Body.Close()
		if p, err := os.FindProcess(pid); err == nil && pid > 0 {
			g.Log.Info("stopping a stream gateway left over from before", "pid", pid)
			p.Kill()
			time.Sleep(500 * time.Millisecond)
		}
	}
}

// Run supervises the gateway until ctx ends.
func (g *Gateway) Run(ctx context.Context) {
	g.mu.Lock()
	g.init()
	g.mu.Unlock()
	g.killOrphan()
	g.unpack()
	backoff := time.Second
	for ctx.Err() == nil {
		g.mu.Lock()
		want := g.want
		g.mu.Unlock()
		bin := g.Binary()
		switch {
		case !want:
			g.setState("off", "")
		case bin == "":
			g.setState("missing", "")
		default:
			started := time.Now()
			err := g.runOnce(ctx, bin)
			if ctx.Err() != nil {
				return
			}
			msg := "the stream gateway stopped"
			if err != nil {
				msg += ": " + err.Error()
			}
			g.setState("failed", msg)
			g.Log.Warn("stream gateway exited", "err", err)
			if time.Since(started) > time.Minute {
				backoff = time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-g.kick:
		}
	}
}

func (g *Gateway) runOnce(ctx context.Context, bin string) error {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, g.configPath())
	cmd.Dir = g.Dir
	w := &lineWriter{g: g}
	cmd.Stdout, cmd.Stderr = w, w
	cmd.WaitDelay = 3 * time.Second
	g.setState("starting", "")
	if err := cmd.Start(); err != nil {
		return err
	}
	os.WriteFile(filepath.Join(g.Dir, "mediamtx.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	defer os.Remove(filepath.Join(g.Dir, "mediamtx.pid"))
	g.mu.Lock()
	g.cmd = cmd
	g.mu.Unlock()
	go g.sampleStats(cctx) // bandwidth, while this process lives
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ready := time.NewTicker(500 * time.Millisecond)
	defer ready.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-g.kick:
			g.mu.Lock()
			want := g.want
			g.mu.Unlock()
			if !want {
				cancel()
				<-done
				return nil
			}
		case <-ready.C:
			if _, err := g.apiPaths(); err == nil {
				g.mu.Lock()
				if g.state == "starting" {
					g.state = "running"
					g.Log.Info("stream gateway running", "version", Version)
				}
				g.mu.Unlock()
			}
		}
	}
}

func (g *Gateway) apiPaths() ([]PathState, error) {
	cl := http.Client{Timeout: time.Second}
	resp, err := cl.Get("http://" + apiAddr + "/v3/paths/list?itemsPerPage=200")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Items []struct {
			Name    string            `json:"name"`
			Ready   bool              `json:"ready"`
			Readers []json.RawMessage `json:"readers"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]PathState, 0, len(body.Items))
	for _, it := range body.Items {
		out = append(out, PathState{Name: it.Name, Ready: it.Ready, Readers: len(it.Readers)})
	}
	return out, nil
}

func (g *Gateway) Status() Status {
	g.mu.Lock()
	st := Status{Managed: g.want, State: g.state, Version: Version, Error: g.err,
		Log: append([]string(nil), g.logs...)}
	if g.installing {
		st.State = "installing"
	}
	g.mu.Unlock()
	if st.State == "" {
		st.State = "off"
	}
	st.Installed = g.Binary()
	_, st.CanFetch = pinned[platform()]
	st.Bundled = Bundled()
	if st.State == "running" {
		st.Paths, _ = g.apiPaths()
	}
	return st
}

// Fetch downloads the pinned MediaMTX release for plat ("linux_amd64",
// …), checks its sha256 against the value compiled into this app, and
// returns the gateway program and its LICENSE text. The release build
// uses it to bundle MediaMTX into ptz; Install uses it at run time.
func Fetch(ctx context.Context, plat string) (bin, license []byte, err error) {
	sum, ok := pinned[plat]
	if !ok {
		return nil, nil, fmt.Errorf("no MediaMTX build is pinned for %s", plat)
	}
	ext := ".tar.gz"
	if strings.HasPrefix(plat, "windows_") {
		ext = ".zip"
	}
	url := "https://github.com/bluenviron/mediamtx/releases/download/" + Version + "/" +
		fmt.Sprintf("mediamtx_%s_%s%s", Version, plat, ext)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("download failed: HTTP %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return nil, nil, err
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != sum {
		return nil, nil, errors.New("the MediaMTX download did not match the pinned checksum — refused")
	}
	exe := "mediamtx"
	if ext == ".zip" {
		exe += ".exe"
	}
	files, err := extract(data, ext, exe, "LICENSE")
	if err != nil {
		return nil, nil, err
	}
	if files[exe] == nil {
		return nil, nil, errors.New("mediamtx not found in the download")
	}
	return files[exe], files["LICENSE"], nil
}

// Install fetches MediaMTX at run time (builds without a bundled copy).
func (g *Gateway) Install(ctx context.Context) error {
	if _, ok := pinned[platform()]; !ok {
		return fmt.Errorf("no MediaMTX build is pinned for %s — install it yourself and put it beside ptz", platform())
	}
	g.mu.Lock()
	if g.installing {
		g.mu.Unlock()
		return errors.New("already downloading")
	}
	g.installing = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.installing = false
		g.mu.Unlock()
		g.poke()
	}()
	g.Log.Info("downloading stream gateway", "version", Version)
	bin, _, err := Fetch(ctx, platform())
	if err != nil {
		return err
	}
	if err := writeExe(filepath.Join(g.Dir, "bin", exeName()), bin); err != nil {
		return err
	}
	g.Log.Info("stream gateway installed", "version", Version)
	return nil
}

func writeExe(dst string, bin []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst+".tmp", bin, 0o755); err != nil {
		return err
	}
	os.Remove(dst) // Windows: rename can't replace
	return os.Rename(dst+".tmp", dst)
}

// extract returns the named files (by base name) from a release archive.
func extract(data []byte, ext string, names ...string) (map[string][]byte, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string][]byte{}
	if ext == ".zip" {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if b := filepath.Base(f.Name); want[b] {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				out[b], err = io.ReadAll(io.LimitReader(rc, 200<<20))
				rc.Close()
				if err != nil {
					return nil, err
				}
			}
		}
		return out, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if b := filepath.Base(h.Name); want[b] && h.Typeflag == tar.TypeReg {
			if out[b], err = io.ReadAll(io.LimitReader(tr, 200<<20)); err != nil {
				return nil, err
			}
		}
	}
}
