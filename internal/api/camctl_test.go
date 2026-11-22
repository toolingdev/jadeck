package api

import (
	"slices"
	"strings"
	"testing"
)

func logHas(log []string, frame string) bool { return slices.Contains(log, frame) }

func TestCameraControls(t *testing.T) {
	e := setup(t)

	code, c, _ := e.do(t, "GET", "/api/camera/controls", nil)
	if code != 200 || c["panSpeed"] != float64(24) || c["recallSpeed"] != float64(0) || c["focusSpeed"] != float64(0) || len(c["builtinScenes"].([]any)) != 3 {
		t.Fatalf("defaults: %d %v", code, c)
	}
	if code, _, _ := e.do(t, "PATCH", "/api/camera/controls", map[string]any{"tiltSpeed": 30}); code != 400 {
		t.Fatalf("tilt 30 must be refused: %d", code)
	}
	code, c, _ = e.do(t, "PATCH", "/api/camera/controls", map[string]any{"panSpeed": 6, "recallSpeed": 9, "focusSpeed": 3, "motionSync": true})
	if code != 200 || c["panSpeed"] != float64(6) || c["motionSync"] != true {
		t.Fatalf("patch: %d %v", code, c)
	}
	if !slices.Contains(e.mock.HTTPCalls(), "post_image_value&motionsync&2") {
		t.Fatalf("motion sync not sent: %v", e.mock.HTTPCalls())
	}
	if l := e.srv.Cams.Main().Svc.Limits(); l.Pan != 6 || l.Recall != 9 || l.Focus != 2 {
		t.Fatalf("limits not applied to the camera: %+v", l)
	}

	// Recall at the camera's recall speed: the speed goes first.
	e.mock.Reset(nil, nil, nil)
	if code, r, _ := e.do(t, "POST", "/api/presets/1/recall", nil); code != 200 {
		t.Fatalf("recall: %d %v", code, r)
	}
	log := e.mock.State().Log
	if len(log) < 2 || log[0] != "8101060109ff" || !strings.HasPrefix(log[1], "8101043f02") {
		t.Fatalf("recall frames: %v", log)
	}

	// Focus lock and the menu.
	e.mock.Reset(nil, nil, nil)
	if code, c, _ = e.do(t, "POST", "/api/camera/focus-lock", map[string]any{"locked": true}); code != 200 || c["focusLocked"] != true {
		t.Fatalf("lock: %d %v", code, c)
	}
	if !logHas(e.mock.State().Log, "810a046802ff") {
		t.Fatalf("lock frame: %v", e.mock.State().Log)
	}
	if code, _, _ := e.do(t, "POST", "/api/camera/osd", map[string]any{"action": "open"}); code != 200 {
		t.Fatalf("osd: %d", code)
	}
	if !slices.Contains(e.mock.HTTPCalls(), "navigate_mode&OSD") {
		t.Fatalf("osd http: %v", e.mock.HTTPCalls())
	}
	if code, _, _ := e.do(t, "POST", "/api/camera/osd", map[string]any{"action": "sideways"}); code != 400 {
		t.Fatalf("bad osd action: %d", code)
	}

	// Scenes: save the picture, change it, put it back.
	e.do(t, "POST", "/api/camera/image", map[string]any{"brightness": 3})
	code, c, _ = e.do(t, "POST", "/api/camera/scenes", map[string]any{"name": "Sermon"})
	if code != 201 {
		t.Fatalf("save scene: %d %v", code, c)
	}
	scenes := c["scenes"].([]any)
	id := scenes[0].(map[string]any)["id"].(string)
	if c["activeScene"] != id {
		t.Fatalf("saved scene should be active: %v", c)
	}
	e.do(t, "POST", "/api/camera/image", map[string]any{"brightness": 9})
	if code, _, _ := e.do(t, "POST", "/api/camera/scenes/"+id+"/apply", nil); code != 200 {
		t.Fatalf("apply: %d", code)
	}
	if _, img, _ := e.do(t, "GET", "/api/camera/image", nil); img["brightness"] != float64(3) {
		t.Fatalf("scene not applied: %v", img)
	}
	if code, c, _ := e.do(t, "POST", "/api/camera/scenes/builtin:lowlight/apply", nil); code != 200 || c["activeScene"] != "builtin:lowlight" {
		t.Fatalf("built-in: %d %v", code, c)
	}
	if code, c, _ := e.do(t, "DELETE", "/api/camera/scenes/"+id, nil); code != 200 || len(c["scenes"].([]any)) != 0 {
		t.Fatalf("delete: %d %v", code, c)
	}

	// A preset with its own picture and recall speed.
	e.do(t, "POST", "/api/camera/image", map[string]any{"brightness": 5})
	if code, r, _ := e.do(t, "POST", "/api/presets/1/store", map[string]any{"picture": true, "recallSpeed": 20}); code != 200 {
		t.Fatalf("store: %d %v", code, r)
	}
	e.do(t, "POST", "/api/camera/image", map[string]any{"brightness": 12})
	e.mock.Reset(nil, nil, nil)
	if code, _, _ := e.do(t, "POST", "/api/presets/1/recall", nil); code != 200 {
		t.Fatal("recall 2")
	}
	if log := e.mock.State().Log; len(log) == 0 || log[0] != "8101060114ff" {
		t.Fatalf("preset's own speed: %v", log)
	}
	if _, img, _ := e.do(t, "GET", "/api/camera/image", nil); img["brightness"] != float64(5) {
		t.Fatalf("preset picture not applied: %v", img)
	}

	// Editing the camera in Settings keeps its controls.
	cam := e.srv.Cams.Main().Cfg
	if code, _, _ := e.do(t, "PUT", "/api/cameras/"+cam.ID, map[string]any{"name": "Renamed", "host": cam.Host, "viscaPort": cam.ViscaPort, "httpPort": cam.HTTPPort}); code != 200 {
		t.Fatalf("put camera: %d", code)
	}
	if _, c, _ := e.do(t, "GET", "/api/camera/controls", nil); c["panSpeed"] != float64(6) {
		t.Fatalf("controls lost on edit: %v", c)
	}
}

// Using any operator control (focus lock here) must not slow Move down:
// untouched speeds stay at the camera's full range, focus at its standard
// drive and preset recall at the camera's own speed.
func TestControlsKeepMoveSpeed(t *testing.T) {
	e := setup(t)
	before := e.srv.Cams.Main().Svc.Limits()
	if code, _, _ := e.do(t, "POST", "/api/camera/focus-lock", map[string]any{"locked": true}); code != 200 {
		t.Fatalf("lock: %d", code)
	}
	if after := e.srv.Cams.Main().Svc.Limits(); after != before {
		t.Fatalf("limits changed by focus lock: %+v -> %+v", before, after)
	}
	e.mock.Reset(nil, nil, nil)
	if code, _, _ := e.do(t, "POST", "/api/presets/1/recall", nil); code != 200 {
		t.Fatalf("recall: %d", code)
	}
	if log := e.mock.State().Log; len(log) == 0 || strings.HasPrefix(log[0], "81010601") {
		t.Fatalf("a preset speed was sent without one being set: %v", log)
	}
}
