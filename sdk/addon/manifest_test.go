package addon

import (
	"strings"
	"testing"
)

func base(api int, w ...WidgetDef) Manifest {
	return Manifest{ID: "t", Name: "T", Version: "1.0.0", APIVersion: api,
		ConnectionTypes: []ConnectionType{{ID: "s", Label: "S", Capabilities: []string{CapSlides}}},
		Actions:         []ActionDef{{ID: "go_to", Label: "Go"}, {ID: "next", Label: "Next"}},
		Widgets:         w}
}

var sample = map[string]any{}

func TestV1ManifestsUnchanged(t *testing.T) {
	// A v1 list widget naming an action that isn't declared was fine
	// before v2 and stays fine.
	m := base(1, WidgetDef{Kind: "list", Type: "l", Label: "L", Bind: map[string]any{"items": "items", "action": "undeclared"}, Sample: sample},
		WidgetDef{Kind: "core", Type: "slides.prevnext", Label: "P", Sample: sample})
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	m.Widgets = append(m.Widgets, WidgetDef{Kind: "image", Type: "i", Label: "I", Bind: map[string]any{"src": "pic"}, Sample: sample})
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "needs apiVersion 2") {
		t.Fatalf("image in a v1 manifest: %v", err)
	}
	m.APIVersion = 3
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "apiVersion 3") {
		t.Fatalf("apiVersion 3: %v", err)
	}
}

func TestV2Widgets(t *testing.T) {
	good := base(2,
		WidgetDef{Kind: "image", Type: "live", Label: "Live", Bind: map[string]any{"src": "live", "action": "next"}, Size: &Size{6, 6}, MinPx: &Size{240, 160}, Sample: sample},
		WidgetDef{Kind: "buttons", Type: "b", Label: "B", Bind: map[string]any{"buttons": []any{map[string]any{"id": "a", "label": "A", "action": "go_to"}}}, Sample: sample},
		WidgetDef{Kind: "buttons", Type: "b2", Label: "B2", Bind: map[string]any{"items": "btns", "action": "next"}, Sample: sample},
		WidgetDef{Kind: "input", Type: "in", Label: "In", Bind: map[string]any{"action": "go_to", "param": "text"}, Sample: sample},
		WidgetDef{Kind: "strip", Type: "st", Label: "St", Bind: map[string]any{"items": "thumbs", "action": "go_to"}, Sample: sample},
		WidgetDef{Kind: "sections", Type: "se", Label: "Se", Bind: map[string]any{"sections": "items", "action": "go_to"}, Sample: sample},
		WidgetDef{Kind: "text", Type: "tx", Label: "Tx", Bind: map[string]any{"body": "notes"}, Sample: sample},
		WidgetDef{Kind: "clock", Type: "cl", Label: "Cl", Sample: sample},
		WidgetDef{Kind: "slider", Type: "sl", Label: "Sl", Bind: map[string]any{"value": "tbar", "action": "next"}, Sample: sample},
		WidgetDef{Kind: "frame", Type: "fr", Label: "Fr", Entry: "ui/live.html", Sample: sample},
		WidgetDef{Kind: "list", Type: "l", Label: "L", Bind: map[string]any{"items": "items", "action": "go_to"}, Sample: sample},
	)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		w    WidgetDef
		want string
	}{
		{WidgetDef{Kind: "image", Type: "x", Label: "X", Bind: map[string]any{"src": "p", "action": "nope"}, Sample: sample}, `bind.action "nope"`},
		{WidgetDef{Kind: "list", Type: "x", Label: "X", Bind: map[string]any{"items": "i", "action": "nope"}, Sample: sample}, `bind.action "nope"`},
		{WidgetDef{Kind: "buttons", Type: "x", Label: "X", Bind: map[string]any{"buttons": []any{map[string]any{"id": "a", "action": "nope"}}}, Sample: sample}, `bind.buttons[0].action "nope"`},
		{WidgetDef{Kind: "buttons", Type: "x", Label: "X", Bind: map[string]any{"buttons": []any{map[string]any{"id": "a"}}}, Sample: sample}, "has no action"},
		{WidgetDef{Kind: "buttons", Type: "x", Label: "X", Bind: map[string]any{}, Sample: sample}, "bind.items"},
		{WidgetDef{Kind: "input", Type: "x", Label: "X", Bind: map[string]any{"action": "go_to"}, Sample: sample}, "bind.param is required"},
		{WidgetDef{Kind: "frame", Type: "x", Label: "X", Sample: sample}, "needs entry"},
		{WidgetDef{Kind: "frame", Type: "x", Label: "X", Entry: "live.html", Sample: sample}, "under ui/"},
		{WidgetDef{Kind: "frame", Type: "x", Label: "X", Entry: "ui/../bin/addon", Sample: sample}, "under ui/"},
		{WidgetDef{Kind: "frame", Type: "x", Label: "X", Entry: "ui/app.js", Sample: sample}, "under ui/"},
		{WidgetDef{Kind: "text", Type: "x", Label: "X", Bind: map[string]any{"body": "b"}, Entry: "ui/a.html", Sample: sample}, "only for frame"},
		{WidgetDef{Kind: "clock", Type: "x", Label: "X"}, "no sample"},
		{WidgetDef{Kind: "clock", Type: "x", Label: "X", Size: &Size{0, 2}, Sample: sample}, "size"},
		{WidgetDef{Kind: "hologram", Type: "x", Label: "X", Sample: sample}, "kind must be one of"},
	}
	for _, c := range cases {
		m := base(2, c.w)
		err := m.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s/%v: want %q, got %v", c.w.Kind, c.w.Bind, c.want, err)
		}
	}
}

func TestDropUnknownWidgets(t *testing.T) {
	m := base(2,
		WidgetDef{Kind: "text", Type: "a", Label: "A", Bind: map[string]any{"body": "b"}, Sample: sample},
		WidgetDef{Kind: "hologram", Type: "b", Label: "B", Sample: sample})
	d := m.DropUnknownWidgets()
	if len(d) != 1 || d[0].Type != "b" || len(m.Widgets) != 1 || m.Widgets[0].Type != "a" {
		t.Fatalf("dropped %v kept %v", d, m.Widgets)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	// v2 kinds in a v1 manifest are unknown for it.
	m1 := base(1, WidgetDef{Kind: "image", Type: "i", Label: "I", Sample: sample})
	if d := m1.DropUnknownWidgets(); len(d) != 1 || len(m1.Widgets) != 0 {
		t.Fatalf("v1: dropped %v kept %v", d, m1.Widgets)
	}
	if m2 := base(2); m2.DropUnknownWidgets() != nil {
		t.Fatal("nothing to drop")
	}
}

func TestUIFileAllowed(t *testing.T) {
	for name, want := range map[string]bool{
		"ui/index.html": true, "ui/a/b.js": true, "ui/s.CSS": true, "ui/f.woff2": true, "ui/d.json": true, "ui/i.png": true, "ui/i.svg": true,
		"ui/x.exe": false, "ui/../x.js": false, "ui//x.js": false, "bin/x.js": false, "ui/x": false, "ui/a\\b.js": false, "ui/x.htm": false,
	} {
		if got := UIFileAllowed(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestLowestAPIVersion(t *testing.T) {
	v1 := Manifest{APIVersion: APIVersion, Widgets: []WidgetDef{{Kind: KindList}, {Kind: KindCore}}}
	if got := v1.LowestAPIVersion().APIVersion; got != 1 {
		t.Fatalf("v1-only widgets: apiVersion %d, want 1", got)
	}
	v2 := Manifest{APIVersion: APIVersion, Widgets: []WidgetDef{{Kind: KindList}, {Kind: KindImage}}}
	if got := v2.LowestAPIVersion().APIVersion; got != 2 {
		t.Fatalf("image widget: apiVersion %d, want 2", got)
	}
	pinned := Manifest{APIVersion: 1, Widgets: []WidgetDef{{Kind: KindImage}}}
	if got := pinned.LowestAPIVersion().APIVersion; got != 1 {
		t.Fatalf("pinned manifest changed to %d", got)
	}
}

func TestToolCapability(t *testing.T) {
	m := base(2)
	m.ConnectionTypes = []ConnectionType{{ID: "x", Label: "X", Capabilities: []string{CapTool}}}
	if err := m.Validate(); err != nil {
		t.Fatalf("tool connection type in a v2 manifest: %v", err)
	}
	// older controllers don't know "tool": it needs apiVersion 2
	m.APIVersion = 1
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "capability tool needs apiVersion 2") {
		t.Fatalf("tool in a v1 manifest: %v", err)
	}
	m.ConnectionTypes[0].Capabilities = []string{"teleporter"}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), `unknown capability "teleporter"`) {
		t.Fatalf("unknown capability: %v", err)
	}
	// LowestAPIVersion keeps a tool add-on at 2 even with only v1 widgets
	low := Manifest{APIVersion: APIVersion, Widgets: []WidgetDef{{Kind: KindList}},
		ConnectionTypes: []ConnectionType{{ID: "x", Capabilities: []string{CapTool}}}}
	if got := low.LowestAPIVersion().APIVersion; got != 2 {
		t.Fatalf("tool connection type: apiVersion %d, want 2", got)
	}
	low.ConnectionTypes[0].Capabilities = []string{CapAssistant}
	if got := low.LowestAPIVersion().APIVersion; got != 1 {
		t.Fatalf("assistant connection type: apiVersion %d, want 1", got)
	}
	if got := strings.Join(Capabilities(1), ","); got != "switcher,slides,mixer,assistant" {
		t.Fatal(got)
	}
	if got := strings.Join(Capabilities(2), ","); !strings.HasSuffix(got, ",tool") {
		t.Fatal(got)
	}
}

func TestWidgetFields(t *testing.T) {
	opts := []Option{{Value: "a", Label: "A"}}
	good := base(2, WidgetDef{Kind: "buttons", Type: "mutes", Label: "Mutes", Sample: sample,
		Bind: map[string]any{"items": "$settings.channels", "action": "next", "columns": "$settings.cols",
			"buttons": []any{map[string]any{"id": "x", "params": map[string]any{"ch": "$settings.channels"}}}},
		Fields: []Field{
			{Key: "channels", Label: "Channels", Type: "order", OptionsFrom: "channels"},
			{Key: "cols", Label: "Columns", Type: "number"},
			{Key: "group", Label: "Group", Type: "select", Multiple: true, Options: opts},
		}})
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	// a v1 manifest's widgets may have fields too (checked the same way)
	v1 := base(1, WidgetDef{Kind: "list", Type: "l", Label: "L", Sample: sample, Bind: map[string]any{"items": "$settings.nope"}})
	if err := v1.Validate(); err == nil || !strings.Contains(err.Error(), "names no field") {
		t.Fatalf("v1 settings ref: %v", err)
	}

	cases := []struct {
		f    []Field
		bind map[string]any
		want string
	}{
		{[]Field{{Key: "a", Type: "text"}, {Key: "a", Type: "text"}}, nil, `"a" is repeated`},
		{[]Field{{Key: "", Type: "text"}}, nil, "has no key"},
		{[]Field{{Key: "connection", Type: "text"}}, nil, "kept by the controller"},
		{[]Field{{Key: "a", Type: "colour"}}, nil, "type must be one of"},
		{[]Field{{Key: "a", Type: "text", Multiple: true}}, nil, "multiple is only for select"},
		{[]Field{{Key: "a", Type: "order"}}, nil, "needs options or optionsFrom"},
		{[]Field{{Key: "a", Type: "text"}}, map[string]any{"body": "$settings.b"}, `bind.body "$settings.b"`},
		{nil, map[string]any{"body": "x", "deep": []any{map[string]any{"k": "$settings.z"}}}, `bind.deep[0].k`},
	}
	for _, c := range cases {
		b := c.bind
		if b == nil {
			b = map[string]any{"body": "notes"}
		}
		m := base(2, WidgetDef{Kind: "text", Type: "w", Label: "W", Sample: sample, Bind: b, Fields: c.f})
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v / %v: want %q, got %v", c.f, c.bind, c.want, err)
		}
	}
}

func TestGuideValidation(t *testing.T) {
	m := base(2)
	m.ConnectionTypes[0].Guide = &Guide{Steps: []GuideStep{{Text: "Open it", Link: &GuideLink{Label: "Open", URL: "http://example.com"}}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "https://") {
		t.Fatalf("http link must be refused: %v", err)
	}
	m.ConnectionTypes[0].Guide.Steps[0].Link.URL = "https://example.com"
	if err := m.Validate(); err != nil {
		t.Fatalf("good guide refused: %v", err)
	}
}
