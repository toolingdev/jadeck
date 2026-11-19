package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "ptz/internal/devices/httppres"
	_ "ptz/internal/devices/midi"
	_ "ptz/internal/devices/osc"
	_ "ptz/internal/devices/vmix"
)

func TestDeviceSetupGUIFlow(t *testing.T) {
	e := setup(t)
	path := filepath.Join(t.TempDir(), "devices.json")
	e.srv.DevicesPath = path

	_, st, _ := e.do(t, "GET", "/api/devices/setup", nil)
	have := map[string]bool{}
	for _, m := range st["metas"].([]any) {
		mm := m.(map[string]any)
		have[mm["kind"].(string)+"/"+mm["driver"].(string)] = true
	}
	for _, want := range []string{"switcher/vmix", "presentation/midi", "presentation/http", "mixer/osc", "mixer/sim"} {
		if !have[want] {
			t.Errorf("driver form missing: %s", want)
		}
	}

	// A bad save is rejected and changes nothing.
	code, r, _ := e.do(t, "PUT", "/api/devices/setup", map[string]any{
		"mixer": map[string]any{"driver": "osc", "preset": "wing"}, // no host
	})
	if code != 400 || r["errors"].(map[string]any)["mixer"] == nil {
		t.Fatalf("%d %v", code, r)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("bad settings were written")
	}

	// Test a device from the form.
	_, r, _ = e.do(t, "POST", "/api/devices/test", map[string]any{"kind": "switcher", "config": map[string]any{"driver": "sim"}})
	if r["ok"] != true {
		t.Fatalf("test: %v", r)
	}
	_, r, _ = e.do(t, "POST", "/api/devices/test", map[string]any{"kind": "switcher", "config": map[string]any{"driver": "vmix", "host": "127.0.0.1", "port": 1}})
	if r["ok"] != false || r["error"] == nil {
		t.Fatalf("unreachable vMix should fail the test: %v", r)
	}

	// Save: no switcher, sim slides, sim mixer, new favorites — applies live.
	code, r, _ = e.do(t, "PUT", "/api/devices/setup", map[string]any{
		"switcher":     map[string]any{"driver": ""},
		"presentation": map[string]any{"driver": "sim", "label": "Practice slides"},
		"mixer":        map[string]any{"driver": "sim", "label": "Practice desk"},
		"favorites":    []any{map[string]any{"label": "Pulpit", "channel": 5}, map[string]any{"label": "Spare", "channel": 0}},
	})
	if code != 200 {
		t.Fatalf("save: %d %v", code, r)
	}
	_, sw, _ := e.do(t, "GET", "/api/switcher/status", nil)
	if sw["connected"] != false || sw["driver"] != "" {
		t.Fatalf("switcher should now be off: %v", sw)
	}
	_, mx, _ := e.do(t, "GET", "/api/mixer/status", nil)
	ch := mx["channels"].([]any)
	if mx["label"] != "Practice desk" || len(ch) != 2 || ch[0].(map[string]any)["label"] != "Pulpit" {
		t.Fatalf("mixer not swapped: %v", mx)
	}
	// Macros use the new devices immediately.
	code, m, _ := e.do(t, "POST", "/api/macros", map[string]any{"name": "Pulpit off", "steps": []any{
		map[string]any{"type": "mixer_mute", "channelName": "pulpit", "muted": true}}})
	if code != 201 {
		t.Fatalf("%d %v", code, m)
	}
	_, rep, _ := e.do(t, "POST", "/api/macros/"+fmtID(m["id"].(float64))+"/run", nil)
	if rep["ok"] != true {
		t.Fatalf("macro on swapped devices: %v", rep)
	}
	// The file the app wrote reloads to the same thing.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	json.Unmarshal(b, &saved)
	if saved["mixer"].(map[string]any)["label"] != "Practice desk" || saved["switcher"] != nil {
		t.Fatalf("saved file: %s", b)
	}
	_, st, _ = e.do(t, "GET", "/api/devices/setup", nil)
	if st["config"].(map[string]any)["presentation"].(map[string]any)["label"] != "Practice slides" {
		t.Fatalf("setup GET after save: %v", st["config"])
	}
	// Keys can't change device setup.
	_, kr, _ := e.do(t, "POST", "/api/keys", map[string]any{"name": "agent", "scopes": []string{"mixer"}})
	if c := e.asOutsider(t, "PUT", "/api/devices/setup", kr["secret"].(string)); c != 403 {
		t.Fatalf("key changed device setup: %d", c)
	}
}
