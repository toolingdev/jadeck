package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"ptz/internal/auth"
	"ptz/internal/camera"
	"ptz/internal/cams"
	"ptz/internal/dashboards"
	"ptz/internal/devices"
	"ptz/internal/devices/sim"
	"ptz/internal/macros"
	"ptz/internal/mockcam"
	"ptz/internal/settings"
	"ptz/internal/update"
	"ptz/internal/video"
	"ptz/web"
)

type env struct {
	url   string
	mock  *mockcam.Mock
	cam   *camera.Service
	mixer *sim.Mixer
	dev   *devices.Set
	srv   *Server
	h     http.Handler
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := mockcam.New()
	m.Log = quiet
	va, ha, err := m.Serve(ctx, "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	vh, p, _ := net.SplitHostPort(va)
	port, _ := strconv.Atoi(p)
	_, hp, _ := net.SplitHostPort(ha)
	httpPort, _ := strconv.Atoi(hp)
	dataDir := t.TempDir()
	setStore, err := settings.Open(filepath.Join(dataDir, "settings.json"), func() settings.Settings {
		c := settings.Camera{ID: "main1", Name: "Sanctuary", Host: vh, ViscaPort: port, HTTPPort: httpPort, Video: true, Presets: "presets.db"}
		c.Defaults()
		return settings.Settings{Cameras: []settings.Camera{c}, MainID: c.ID, SetupDone: true,
			Video: settings.Video{Managed: true}, System: settings.System{Port: 8080, TrustLAN: true}}
	})
	if err != nil {
		t.Fatal(err)
	}
	reg := cams.New(ctx, dataDir, quiet)
	t.Cleanup(reg.Close)
	gw := &video.Gateway{Dir: filepath.Join(dataDir, "video"), Log: quiet}
	ms, err := macros.OpenStore(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ms.Close() })
	simMixer := sim.NewMixer(48)
	dev := &devices.Set{Switcher: sim.NewSwitcher(), Presentation: sim.NewPresentation(nil), Mixer: simMixer,
		SwitcherCfg: devices.DeviceConfig{Driver: "sim"}, PresentationCfg: devices.DeviceConfig{Driver: "sim"},
		MixerCfg: devices.DeviceConfig{Driver: "sim"}}
	one, eight := 1, 8
	dev.Favorites = []devices.Slot{{Slot: 1, Label: "MAIN", Channel: &one}, {Slot: 2, Label: "VOX", Channel: &eight}, {Slot: 3, Label: "Spare"}}
	um := &update.Manager{Current: "test", Dir: t.TempDir(), Source: &update.Source{}, Log: quiet}
	keys, err := auth.OpenStore(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { keys.Close() })
	trust, _ := auth.NewTrust(true, "")
	dash, err := dashboards.Open(filepath.Join(t.TempDir(), "dash.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dash.Close() })
	srv := &Server{Cams: reg, Settings: setStore, Video: gw, Mock: true, Web: web.Dist, Legacy: web.Legacy, Log: quiet, Version: "test", Keys: keys, Trust: trust, Dashboards: dash,
		Dev: dev, MacroStore: ms, Updates: um, Restart: Restarter{Mode: "exec"}}
	srv.Macros = macros.NewEngine(ms, srv.MacroActions())
	h := srv.Handler()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	reg.Apply(setStore.Get())
	setStore.OnChange(func(_, next settings.Settings) { srv.ApplySettings(next) })
	cam := reg.Main().Svc
	for i := 0; i < 250 && !cam.Connected(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	return &env{url: ts.URL, mock: m, cam: cam, mixer: simMixer, dev: dev, srv: srv, h: h}
}

func (e *env) do(t *testing.T, method, path string, body any) (int, map[string]any, []any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	var arr []any
	if json.Unmarshal(raw, &obj) != nil {
		json.Unmarshal(raw, &arr)
	}
	return resp.StatusCode, obj, arr
}

func TestPages(t *testing.T) {
	e := setup(t)
	for _, p := range []string{"/", "/dashboard", "/settings", "/sw.js", "/manifest.webmanifest", "/icon.svg",
		"/legacy", "/legacy/dashboard", "/static/app.js", "/static/style.css"} {
		resp, err := http.Get(e.url + p)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, err, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestStatusAndConfig(t *testing.T) {
	e := setup(t)
	code, st, _ := e.do(t, "GET", "/api/status", nil)
	if code != 200 || st["connected"] != true || st["device"].(map[string]any)["devtype"] != "V63C" {
		t.Fatalf("%d %v", code, st)
	}
	_, cfg, _ := e.do(t, "GET", "/api/config", nil)
	if cfg["whep"] != nil || cfg["snapshot"] != "/api/snapshot?cam=main1" {
		t.Fatalf("no gateway running yet, so stills only: %v", cfg)
	}
	e.do(t, "PUT", "/api/video", map[string]any{"managed": false, "external": "127.0.0.1"})
	_, cfg, _ = e.do(t, "GET", "/api/config", nil)
	if cfg["whep"] != "http://127.0.0.1:8889/cam/whep" || cfg["programWhep"] == nil {
		t.Fatalf("own gateway: %v", cfg)
	}
	resp, _ := http.Get(e.url + "/api/snapshot")
	b, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Content-Type") != "image/jpeg" || len(b) < 100 {
		t.Fatal("snapshot")
	}
}

func TestPresetFlow(t *testing.T) {
	e := setup(t)
	_, _, list := e.do(t, "GET", "/api/presets", nil)
	if len(list) != 8 {
		t.Fatalf("seed: %v", list)
	}
	code, p, _ := e.do(t, "POST", "/api/presets", map[string]any{"name": "  Pulpit "})
	if code != 201 || p["name"] != "Pulpit" || p["slot"].(float64) != 9 {
		t.Fatalf("%d %v", code, p)
	}
	if code, _, _ := e.do(t, "POST", "/api/presets", map[string]any{"name": ""}); code != 422 {
		t.Fatalf("empty name: %d", code)
	}
	if code, _, _ := e.do(t, "POST", "/api/presets", map[string]any{"name": "x", "slot": 200}); code != 422 {
		t.Fatalf("bad slot: %d", code)
	}
	id := strconv.Itoa(int(p["id"].(float64)))
	pan := 321
	e.mock.Reset(&pan, nil, nil)
	if code, r, _ := e.do(t, "POST", "/api/presets/"+id+"/store", nil); code != 200 || r["slot"].(float64) != 9 {
		t.Fatalf("store %d %v", code, r)
	}
	e.do(t, "POST", "/api/ptz/home", nil)
	e.do(t, "POST", "/api/presets/"+id+"/recall", nil)
	if e.mock.State().Pan != 321 {
		t.Fatal("recall did not restore position")
	}
	if code, _, _ := e.do(t, "POST", "/api/presets/9999/recall", nil); code != 404 {
		t.Fatalf("missing: %d", code)
	}
	code, p, _ = e.do(t, "PATCH", "/api/presets/"+id, map[string]any{"name": "Pulpit Wide"})
	if code != 200 || p["name"] != "Pulpit Wide" {
		t.Fatalf("patch %d %v", code, p)
	}
	ids := []int{int(p["id"].(float64))} // UI sends the full order
	_, _, all := e.do(t, "GET", "/api/presets", nil)
	for _, x := range all {
		if xid := int(x.(map[string]any)["id"].(float64)); xid != ids[0] {
			ids = append(ids, xid)
		}
	}
	_, _, list = e.do(t, "POST", "/api/presets/reorder", map[string]any{"ids": ids})
	if list[0].(map[string]any)["name"] != "Pulpit Wide" {
		t.Fatalf("reorder %v", list[0])
	}
	if code, _, _ := e.do(t, "DELETE", "/api/presets/"+id, nil); code != 200 {
		t.Fatal("delete")
	}
}

func TestFocusAndImage(t *testing.T) {
	e := setup(t)
	if code, _, _ := e.do(t, "POST", "/api/focus", map[string]any{"action": "zoom"}); code != 400 {
		t.Fatal("bad focus action accepted")
	}
	if code, _, _ := e.do(t, "POST", "/api/focus", map[string]any{"action": "manual"}); code != 200 {
		t.Fatal("focus manual")
	}
	_, img, _ := e.do(t, "GET", "/api/camera/image", nil)
	if img["brightness"].(float64) != 11 {
		t.Fatalf("image %v", img)
	}
	code, img, _ := e.do(t, "POST", "/api/camera/image", map[string]any{"brightness": 6, "flip": true, "hue": nil})
	if code != 200 || img["brightness"].(float64) != 6 || img["flip"] != true || img["hue"].(float64) != 8 {
		t.Fatalf("%d %v", code, img)
	}
	if code, _, _ := e.do(t, "POST", "/api/camera/image", map[string]any{"brightness": 99}); code != 400 {
		t.Fatal("out of range accepted")
	}
	if code, _, _ := e.do(t, "POST", "/api/camera/image", map[string]any{}); code != 400 {
		t.Fatal("empty accepted")
	}
}

func TestWBPickEndpoint(t *testing.T) {
	e := setup(t)
	z := 0
	e.mock.Reset(nil, nil, &z)
	time.Sleep(800 * time.Millisecond) // let the poll see zoom 0
	if code, _, _ := e.do(t, "POST", "/api/camera/wb-pick", map[string]any{"x": 1.5, "y": 0.5}); code != 422 {
		t.Fatal("out of range tap accepted")
	}
	code, r, _ := e.do(t, "POST", "/api/camera/wb-pick", map[string]any{"x": 0.7, "y": 0.4})
	if code != 200 || r["ok"] != true {
		t.Fatalf("%d %v", code, r)
	}
	if e.mock.State().WBVISCATriggers != 1 {
		t.Fatal("no trigger")
	}
}

func TestWebSocketDrive(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(e.url, "http", "ws", 1)+"/ws/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := c.Read(ctx)
	if err != nil || !bytes.Contains(first, []byte(`"t":"status"`)) {
		t.Fatalf("first message: %s %v", first, err)
	}
	// Garbage must not kill the socket (Python closed it).
	c.Write(ctx, websocket.MessageText, []byte(`{"t":"drive","pan":"left"}`))
	c.Write(ctx, websocket.MessageText, []byte(`not json`))
	c.Write(ctx, websocket.MessageText, []byte(`{"t":"drive","pan":1,"speed":80}`))
	time.Sleep(300 * time.Millisecond)
	if !e.cam.Driving() {
		t.Fatal("not driving after a valid drive message")
	}
	// Abrupt disconnect stops the camera immediately (no dead-man wait).
	c.CloseNow()
	time.Sleep(250 * time.Millisecond)
	if e.cam.Driving() {
		t.Fatal("still driving after the driver disconnected")
	}
}

func TestSwitcherRoutesAndAliases(t *testing.T) {
	e := setup(t)
	for _, p := range []string{"switcher", "vmix"} {
		_, st, _ := e.do(t, "GET", "/api/"+p+"/status", nil)
		if st["connected"] != true || st["simulated"] != true || len(st["inputs"].([]any)) != 4 {
			t.Fatalf("%s: %v", p, st)
		}
	}
	code, st, _ := e.do(t, "POST", "/api/vmix/preview", map[string]any{"input": 3})
	if code != 200 || st["preview"].(float64) != 3 {
		t.Fatalf("%d %v", code, st)
	}
	_, st, _ = e.do(t, "POST", "/api/switcher/cut", nil)
	if st["active"].(float64) != 3 {
		t.Fatalf("cut: %v", st)
	}
	if code, _, _ := e.do(t, "POST", "/api/vmix/fade", nil); code != 200 {
		t.Fatalf("fade with no body: %d", code)
	}
	if code, _, _ := e.do(t, "POST", "/api/switcher/preview", map[string]any{"input": 99}); code != 502 {
		t.Fatalf("unknown input: %d", code)
	}
}

func TestPresentationRoutes(t *testing.T) {
	e := setup(t)
	_, st, _ := e.do(t, "GET", "/api/proclaim/status", nil)
	if st["available"] != true || len(st["actions"].([]any)) != 5 {
		t.Fatalf("%v", st)
	}
	code, r, _ := e.do(t, "POST", "/api/presentation/next", nil)
	if code != 200 || r["ok"] != true || r["action"] != "next" {
		t.Fatalf("%d %v", code, r)
	}
	if code, _, _ := e.do(t, "POST", "/api/proclaim/explode", nil); code != 400 {
		t.Fatalf("unknown action: %d", code)
	}
}

func TestMixerRoutes(t *testing.T) {
	e := setup(t)
	_, st, _ := e.do(t, "GET", "/api/wing/status", nil)
	chans := st["channels"].([]any)
	if st["connected"] != true || len(chans) != 3 || chans[0].(map[string]any)["muted"] != false {
		t.Fatalf("%v", st)
	}
	if chans[2].(map[string]any)["channel"] != nil {
		t.Fatal("placeholder slot should have no channel")
	}
	code, r, _ := e.do(t, "POST", "/api/wing/mute", map[string]any{"slot": 1}) // toggle
	if code != 200 || r["muted"] != true {
		t.Fatalf("%d %v", code, r)
	}
	code, r, _ = e.do(t, "POST", "/api/mixer/mute", map[string]any{"slot": 1, "muted": false})
	if code != 200 || r["muted"] != false {
		t.Fatalf("%d %v", code, r)
	}
	if code, _, _ := e.do(t, "POST", "/api/mixer/mute", map[string]any{"slot": 3}); code != 409 {
		t.Fatalf("placeholder: %d", code)
	}
	e.mixer.StuckChannels[8] = true
	if code, r, _ := e.do(t, "POST", "/api/mixer/mute", map[string]any{"slot": 2, "muted": true}); code != 502 ||
		!strings.Contains(r["detail"].(string), "did not take") {
		t.Fatalf("read-back mismatch must fail: %d %v", code, r)
	}
	code, r, _ = e.do(t, "POST", "/api/mixer/channel-mute", map[string]any{"channel": 20, "muted": true})
	if code != 200 || r["muted"] != true {
		t.Fatalf("%d %v", code, r)
	}
}

func TestMacroRoutes(t *testing.T) {
	e := setup(t)
	pan := 777
	e.mock.Reset(&pan, nil, nil)
	e.do(t, "POST", "/api/presets/1/store", nil) // "Main" preset = pan 777
	e.do(t, "POST", "/api/ptz/home", nil)
	code, m, _ := e.do(t, "POST", "/api/macros", map[string]any{"name": "Sermon start", "steps": []any{
		map[string]any{"type": "camera_preset", "preset": "main"},
		map[string]any{"type": "wing_mute", "channelName": "vox", "muted": true},
		map[string]any{"type": "mixer_mute", "channelName": "MAIN", "muted": false},
		map[string]any{"type": "switcher", "action": "preview", "input": 2},
		map[string]any{"type": "switcher", "action": "cut"},
		map[string]any{"type": "slides", "action": "black"},
	}})
	if code != 201 {
		t.Fatalf("%d %v", code, m)
	}
	id := strconv.Itoa(int(m["id"].(float64)))
	code, rep, _ := e.do(t, "POST", "/api/macros/"+id+"/run", nil)
	if code != 200 || rep["ok"] != true {
		t.Fatalf("%d %v", code, rep)
	}
	if e.mock.State().Pan != 777 {
		t.Fatal("camera_preset step did not move the camera")
	}
	if muted, _ := e.mixer.GetMute(context.Background(), 8); !muted {
		t.Fatal("VOX not muted")
	}
	// Validation errors are 400 with a step-numbered message.
	code, r, _ := e.do(t, "POST", "/api/macros", map[string]any{"name": "Bad", "steps": []any{
		map[string]any{"type": "mixer_mute", "channelName": "Spare", "muted": true}}})
	if code != 400 || !strings.Contains(r["detail"].(string), "step 1") {
		t.Fatalf("%d %v", code, r)
	}
	// Duplicate names conflict; patch merges and revalidates.
	if code, _, _ := e.do(t, "POST", "/api/macros", map[string]any{"name": "sermon START", "steps": []any{map[string]any{"type": "delay", "ms": 1}}}); code != 409 {
		t.Fatalf("dup: %d", code)
	}
	code, m, _ = e.do(t, "PATCH", "/api/macros/"+id, map[string]any{"description": "go"})
	if code != 200 || m["description"] != "go" || len(m["steps"].([]any)) != 6 {
		t.Fatalf("patch %d %v", code, m)
	}
	// Import: all-or-nothing.
	code, r, _ = e.do(t, "POST", "/api/macros/import", []any{
		map[string]any{"name": "A", "steps": []any{map[string]any{"type": "delay", "ms": 5}}},
		map[string]any{"name": "B", "steps": []any{map[string]any{"type": "nope"}}},
	})
	if code != 400 || len(r["errors"].([]any)) != 1 {
		t.Fatalf("%d %v", code, r)
	}
	if _, _, l := e.do(t, "GET", "/api/macros", nil); len(l) != 1 {
		t.Fatalf("partial import happened: %v", l)
	}
	code, r, _ = e.do(t, "POST", "/api/macros/import?validateOnly=true", map[string]any{"macros": []any{
		map[string]any{"name": "A", "steps": []any{map[string]any{"type": "delay", "ms": 5}}}}})
	if code != 200 || r["valid"] != true {
		t.Fatalf("%d %v", code, r)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/macros/"+id, nil); code != 200 {
		t.Fatal("delete")
	}
	if code, _, _ := e.do(t, "POST", "/api/macros/"+id+"/run", nil); code != 404 {
		t.Fatal("run deleted")
	}
}

func TestUpdateDisabledWithoutRepo(t *testing.T) {
	e := setup(t)
	_, st, _ := e.do(t, "GET", "/api/update/status", nil)
	if st["state"] != "disabled" {
		t.Fatalf("%v", st)
	}
	if code, _, _ := e.do(t, "POST", "/api/update/apply", nil); code != 409 {
		t.Fatalf("apply: %d", code)
	}
}

func TestDevicesInfo(t *testing.T) {
	e := setup(t)
	_, d, _ := e.do(t, "GET", "/api/devices", nil)
	if d["mixer"].(map[string]any)["ready"] != true {
		t.Fatalf("%v", d)
	}
}
