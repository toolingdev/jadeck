package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ptz/internal/devices"
	"ptz/internal/devices/fake"

	"github.com/toolingdev/ptz-controller/sdk/addon"
)

// gearSlides is a slides connection with its own state and actions, like
// the Proclaim add-on, and a password (write-only in the API).
type gearSlides struct {
	*fake.Presentation
	pw   string
	item int
}

func (g *gearSlides) AddonState(context.Context) (map[string]any, error) {
	return map[string]any{"onAir": true, "currentIndex": g.item, "items": []map[string]any{{"index": 1, "title": "Welcome"}, {"index": 2, "title": "Sermon"}}}, nil
}

func (g *gearSlides) AddonAction(_ context.Context, name string, params json.RawMessage) (any, error) {
	if name != "go_to_item" {
		return nil, fmt.Errorf("%w %q", addon.ErrUnknownAction, name)
	}
	var p struct{ Index int }
	json.Unmarshal(params, &p)
	g.item = p.Index
	return map[string]any{"item": p.Index}, nil
}

// testAddon is a small add-on run inside the test process.
func testAddon() addon.Spec {
	return addon.Spec{
		Manifest: addon.Manifest{ID: "testgear", Name: "Test gear", Version: "1.2.0", Publisher: "tests", APIVersion: addon.APIVersion,
			Description: "Fake gear for the API tests.",
			ConnectionTypes: []addon.ConnectionType{
				{ID: "sw", Label: "Test switcher", Capabilities: []string{addon.CapSwitcher}, Fields: []addon.Field{}},
				{ID: "slides", Label: "Test slides", Capabilities: []string{addon.CapSlides}, Fields: []addon.Field{
					{Key: "host", Label: "Address", Type: "text", Required: true},
					{Key: "password", Label: "Password", Type: "password"},
				}},
				{ID: "mixer", Label: "Test mixer", Capabilities: []string{addon.CapMixer}, Fields: []addon.Field{}},
			},
			Actions: []addon.ActionDef{{ID: "go_to_item", Label: "Go to item", MacroStep: true}},
			Widgets: []addon.WidgetDef{{Kind: "list", Type: "items", Label: "Items", Sample: map[string]any{"items": []any{}}}},
		},
		Open: map[string]addon.OpenFunc{
			"sw": func(json.RawMessage) (any, error) { return fake.NewSwitcher(), nil },
			"slides": func(raw json.RawMessage) (any, error) {
				var c struct{ Host, Password string }
				json.Unmarshal(raw, &c)
				if c.Host == "down" {
					return nil, fmt.Errorf("not answering")
				}
				return &gearSlides{Presentation: fake.NewPresentation(nil), pw: c.Password}, nil
			},
			"mixer": func(json.RawMessage) (any, error) { return fake.NewMixer(48), nil },
		},
	}
}

func TestConnectionsFlow(t *testing.T) {
	e := setup(t)
	path := filepath.Join(t.TempDir(), "devices.json")
	e.srv.DevicesPath = path

	// The add-on is listed with what it adds.
	_, r, _ := e.do(t, "GET", "/api/addons", nil)
	list := r["addons"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["info"].(map[string]any)["manifest"].(map[string]any)["id"] != "testgear" {
		t.Fatalf("addons: %v", r)
	}

	// Required field missing: rejected, nothing written.
	code, r, _ := e.do(t, "POST", "/api/connections", map[string]any{"addon": "testgear", "type": "slides", "name": "Slides A", "config": map[string]any{}})
	if code != 422 || !strings.Contains(r["detail"].(string), "Address") {
		t.Fatalf("%d %v", code, r)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a bad connection was saved")
	}

	// Two slides connections of the same kind, plus a switcher and a mixer.
	add := func(typ, name string, cfg map[string]any) map[string]any {
		code, r, _ := e.do(t, "POST", "/api/connections", map[string]any{"addon": "testgear", "type": typ, "name": name, "config": cfg,
			"favorites": []map[string]any{{"label": "Main", "channel": 1}}})
		if code != 201 {
			t.Fatalf("add %s: %d %v", name, code, r)
		}
		return r
	}
	a := add("slides", "Proclaim — office PC", map[string]any{"host": "10.0.0.1", "password": "s3cret"})
	b := add("slides", "Proclaim — sanctuary PC", map[string]any{"host": "down"})
	add("sw", "vMix", nil)
	add("mixer", "WING", nil)
	if a["ready"] != true || b["ready"] != false || !strings.Contains(b["error"].(string), "not answering") {
		t.Fatalf("a %v / b %v", a, b)
	}
	// Passwords never come back.
	cfg := a["config"].(map[string]any)
	if cfg["password"] != "" || cfg["_has_password"] != true {
		t.Fatalf("password leaked: %v", cfg)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"version": 2`) || !strings.Contains(string(raw), "s3cret") {
		t.Fatalf("saved file: %s", raw)
	}

	// Editing without retyping the password keeps it.
	id := a["id"].(string)
	code, r, _ = e.do(t, "PUT", "/api/connections/"+id, map[string]any{"name": "Proclaim — office", "config": map[string]any{"host": "10.0.0.2", "password": ""}})
	if code != 200 || r["name"] != "Proclaim — office" {
		t.Fatalf("%d %v", code, r)
	}
	if g := e.srv.Devs().Get(id).Impl.(*gearSlides); g.pw != "s3cret" {
		t.Fatalf("password lost on edit: %q", g.pw)
	}

	// The first of each kind is the default: the old routes keep working.
	if code, _, _ := e.do(t, "POST", "/api/switcher/cut", nil); code != 200 {
		t.Fatalf("cut: %d", code)
	}
	if code, r, _ := e.do(t, "POST", "/api/presentation/next", nil); code != 200 {
		t.Fatalf("next: %d %v", code, r)
	}

	// Add-on state and actions.
	code, r, _ = e.do(t, "POST", "/api/connections/"+id+"/actions/go_to_item", map[string]any{"index": 2})
	if code != 200 {
		t.Fatalf("action: %d %v", code, r)
	}
	_, r, _ = e.do(t, "GET", "/api/connections/"+id+"/state", nil)
	if r["currentIndex"].(float64) != 2 {
		t.Fatalf("state %v", r)
	}
	if code, _, _ := e.do(t, "POST", "/api/connections/"+id+"/actions/dance", nil); code != 400 {
		t.Fatalf("unknown action: %d", code)
	}

	// Test without saving.
	_, r, _ = e.do(t, "POST", "/api/connections/test", map[string]any{"addon": "testgear", "type": "slides", "config": map[string]any{"host": "down"}})
	if r["ok"] != false {
		t.Fatalf("test of a down host: %v", r)
	}
	_, r, _ = e.do(t, "POST", "/api/connections/test", map[string]any{"addon": "testgear", "type": "sw"})
	if r["ok"] != true {
		t.Fatalf("test of a switcher: %v", r)
	}

	// Delete.
	if code, _, _ := e.do(t, "DELETE", "/api/connections/"+b["id"].(string), nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	_, r, _ = e.do(t, "GET", "/api/connections", nil)
	if n := len(r["connections"].([]any)); n != 3 {
		t.Fatalf("%d connections left", n)
	}
}

func TestDevicesV1Migrates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "devices.json")
	os.WriteFile(p, []byte(`{"switcher":{"driver":"vmix","label":"vMix","host":"10.0.0.5","port":8088},
		"presentation":{"driver":"midi","label":"Proclaim","port":"loopMIDI"},
		"mixer":{"driver":"osc","label":"WING","preset":"wing","host":"10.0.0.9"},
		"favorites":[{"label":"Main","channel":1}]}`), 0o644)
	f, err := devices.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 2 || len(f.Connections) != 3 {
		t.Fatalf("%+v", f)
	}
	want := map[string]string{"vmix": "vmix", "midi": "midi", "behringer-wing": "osc"}
	for _, c := range f.Connections {
		if want[c.Addon] != c.Type {
			t.Errorf("%s → %s/%s", c.Name, c.Addon, c.Type)
		}
		if strings.Contains(string(c.Config), "driver") {
			t.Errorf("%s config still has the driver key: %s", c.Name, c.Config)
		}
	}
	if f.Connections[2].Favorites[0].Label != "Main" || !strings.Contains(string(f.Connections[0].Config), "10.0.0.5") {
		t.Fatalf("favorites/config lost: %+v", f.Connections)
	}
}
