package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"ptz/internal/mockcam"
	"ptz/internal/video"
)

func (e *env) camCall(t *testing.T, cam, method, path string) (int, []any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.url+path, nil)
	req.Header.Set("X-PTZ-Camera", cam)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _, arr := e.decode(resp)
	return resp.StatusCode, arr
}

func (e *env) decode(resp *http.Response) (int, map[string]any, []any) {
	raw, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	var arr []any
	if jsonUnmarshal(raw, &obj) != nil {
		jsonUnmarshal(raw, &arr)
	}
	return resp.StatusCode, obj, arr
}

func secondMock(t *testing.T) (host string, visca, httpPort int, m *mockcam.Mock) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m = mockcam.New()
	m.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	va, ha, err := m.Serve(ctx, "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h, vp, _ := net.SplitHostPort(va)
	_, hp, _ := net.SplitHostPort(ha)
	visca, _ = strconv.Atoi(vp)
	httpPort, _ = strconv.Atoi(hp)
	return h, visca, httpPort, m
}

func TestCameraTest(t *testing.T) {
	e := setup(t)
	main := e.srv.Settings.Get().Cameras[0]
	code, r, _ := e.do(t, "POST", "/api/cameras/test", map[string]any{"host": main.Host, "viscaPort": main.ViscaPort, "httpPort": main.HTTPPort})
	if code != 200 || r["ok"] != true || r["control"] != true || r["picture"] != true {
		t.Fatalf("test of a live camera: %d %v", code, r)
	}
	if !strings.Contains(r["model"].(string), "MockCam") || r["firmware"] != "6.3.45" {
		t.Fatalf("model/firmware: %v %v", r["model"], r["firmware"])
	}
	if !strings.HasPrefix(r["snapshot"].(string), "data:image/jpeg;base64,") {
		t.Fatal("no snapshot preview")
	}
	// Nothing listening.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := l.Addr().(*net.TCPAddr).Port
	l.Close()
	code, r, _ = e.do(t, "POST", "/api/cameras/test", map[string]any{"host": "127.0.0.1", "viscaPort": dead, "httpPort": dead})
	if code != 200 || r["ok"] != false || r["control"] != false {
		t.Fatalf("dead camera reported ok: %v", r)
	}
}

func TestAddSecondCamera(t *testing.T) {
	e := setup(t)
	if code, _, _ := e.do(t, "POST", "/api/cameras", map[string]any{"name": "", "host": "1.2.3.4"}); code != 422 {
		t.Fatalf("blank name: %d", code)
	}
	host, vp, hp, m2 := secondMock(t)
	code, _, list := e.do(t, "POST", "/api/cameras", map[string]any{"name": "Balcony", "host": host, "viscaPort": vp, "httpPort": hp, "user": "admin", "pass": "s3cret"})
	if code != 201 || len(list) != 2 {
		t.Fatalf("add: %d %v", code, list)
	}
	second := list[1].(map[string]any)
	if second["pass"] != nil || second["hasPassword"] != true || second["main"] != false {
		t.Fatalf("password leaked or flags wrong: %v", second)
	}
	id := second["id"].(string)
	if code, _, _ := e.do(t, "POST", "/api/cameras", map[string]any{"name": "Dup", "host": host, "viscaPort": vp}); code != 409 {
		t.Fatalf("duplicate camera: %d", code)
	}
	u := e.srv.Cams.Get(id)
	for i := 0; i < 250 && (u == nil || !u.Svc.Connected()); i++ {
		time.Sleep(20 * time.Millisecond)
		u = e.srv.Cams.Get(id)
	}
	if u == nil || !u.Svc.Connected() {
		t.Fatal("second camera never connected")
	}
	// Presets are per camera: the new one starts empty, the main keeps its names.
	if _, l := e.camCall(t, id, "GET", "/api/presets"); len(l) != 0 {
		t.Fatalf("second camera presets: %v", l)
	}
	if _, l := e.camCall(t, "", "GET", "/api/presets"); len(l) != 8 {
		t.Fatalf("main camera presets: %d", len(l))
	}
	// Home goes to the camera asked for.
	pan, tilt, zoom := 1000, 500, 0
	e.mock.Reset(&pan, &tilt, &zoom)
	m2.Reset(&pan, &tilt, &zoom)
	if code, _ := e.camCall(t, id, "POST", "/api/ptz/home"); code != 200 {
		t.Fatalf("home second: %d", code)
	}
	time.Sleep(300 * time.Millisecond)
	if st := m2.State(); st.Pan != 0 {
		t.Fatalf("second camera did not go home: %v", st)
	}
	if st := e.mock.State(); st.Pan == 0 {
		t.Fatalf("main camera moved instead: %v", st)
	}
	// Stream paths per camera.
	e.do(t, "PUT", "/api/video", map[string]any{"managed": false, "external": "127.0.0.1"})
	_, cfg, _ := e.do(t, "GET", "/api/config?cam="+id, nil)
	if !strings.Contains(cfg["whep"].(string), video.PathHD(id)+"/whep") || cfg["snapshot"] != "/api/snapshot?cam="+id {
		t.Fatalf("config: %v", cfg)
	}
	if conf := video.Config(e.srv.Settings.Get()); !strings.Contains(conf, "rtsp://admin:s3cret@") || !strings.Contains(conf, video.PathSD(id)) {
		t.Fatalf("gateway config:\n%s", conf)
	}
	// Edit without a password keeps it; removing the main promotes the other.
	if code, _, _ := e.do(t, "PUT", "/api/cameras/"+id, map[string]any{"name": "Balcony 2", "host": host, "viscaPort": vp, "httpPort": hp, "user": "admin"}); code != 200 {
		t.Fatalf("edit: %d", code)
	}
	set := e.srv.Settings.Get()
	if c := set.Camera(id); c.Pass != "s3cret" || c.Name != "Balcony 2" {
		t.Fatalf("edit lost password or name: %+v", c)
	}
	if code, _, list := e.do(t, "DELETE", "/api/cameras/main1", nil); code != 200 || len(list) != 1 || list[0].(map[string]any)["main"] != true {
		t.Fatalf("delete main: %d %v", code, list)
	}
	if e.srv.Cams.MainID() != id {
		t.Fatal("main camera not reassigned")
	}
}

func TestScanFindsMock(t *testing.T) {
	e := setup(t)
	code, r, _ := e.do(t, "GET", "/api/cameras/scan", nil)
	if code != 200 {
		t.Fatalf("scan: %d", code)
	}
	_ = r // the fixed mock ports are not running in tests; the scan must just finish
}

func TestSystemSettings(t *testing.T) {
	e := setup(t)
	if code, _, _ := e.do(t, "PUT", "/api/system", map[string]any{"port": 8080, "trustLan": true, "trustedCidrs": []string{"not-a-net"}}); code != 422 {
		t.Fatalf("bad cidr: %d", code)
	}
	code, r, _ := e.do(t, "PUT", "/api/system", map[string]any{"port": 8080, "name": "Sanctuary-PTZ.local", "trustLan": false,
		"trustedCidrs": []string{"203.0.113.7"}, "updateRepo": "venue/ptz", "updateToken": "tok"})
	if code != 200 {
		t.Fatalf("save: %d %v", code, r)
	}
	sys := r["system"].(map[string]any)
	if sys["name"] != "sanctuary-ptz" || sys["updateToken"] != nil || r["hasToken"] != true {
		t.Fatalf("system: %v", r)
	}
	if !e.srv.Trust.Trusted("203.0.113.7:5000") || e.srv.Trust.Trusted("10.1.10.5:5000") {
		t.Fatal("trust not applied live")
	}
	// Token kept when left blank, cleared on request.
	e.do(t, "PUT", "/api/system", map[string]any{"port": 8080, "trustLan": true, "updateRepo": "venue/ptz"})
	if e.srv.Settings.Get().System.UpdateToken != "tok" {
		t.Fatal("blank token wiped the saved one")
	}
	e.do(t, "PUT", "/api/system", map[string]any{"port": 8080, "trustLan": true, "clearToken": true})
	if e.srv.Settings.Get().System.UpdateToken != "" {
		t.Fatal("token not cleared")
	}
	resp, err := http.Get(e.url + "/api/system/qr?text=http://ptz.local:8080")
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("qr: %v %v", err, resp.StatusCode)
	}
	resp.Body.Close()
}

func TestNoCameraYet(t *testing.T) {
	e := setup(t)
	e.do(t, "DELETE", "/api/cameras/main1", nil)
	if code, _, _ := e.do(t, "GET", "/api/status", nil); code != 503 {
		t.Fatalf("status with no camera: %d", code)
	}
	if code, r, _ := e.do(t, "GET", "/api/setup", nil); code != 200 || r["cameras"] != float64(0) {
		t.Fatalf("setup: %v", r)
	}
	if code, _, _ := e.do(t, "GET", "/api/config", nil); code != 200 {
		t.Fatalf("config with no camera: %d", code)
	}
}

func TestQuietTime(t *testing.T) {
	sat := time.Date(2026, 11, 21, 10, 0, 0, 0, time.Local)
	if QuietTime(sat) || !QuietTime(sat.Add(10*time.Hour)) || !QuietTime(sat.Add(-48*time.Hour)) {
		t.Fatal("quiet window wrong")
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
