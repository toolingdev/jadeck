package addontest_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/toolingdev/jadeck/sdk/addon"
	"github.com/toolingdev/jadeck/sdk/addon/addontest"
)

type slides struct{ sent []string }

func (s *slides) Info() addon.PresentationInfo {
	return addon.PresentationInfo{Available: true, Target: "test"}
}
func (s *slides) Actions() []string { return []string{"next"} }
func (s *slides) Do(_ context.Context, a string) (map[string]any, error) {
	s.sent = append(s.sent, a)
	return map[string]any{"action": a}, nil
}
func (s *slides) AddonState(context.Context) (map[string]any, error) {
	return map[string]any{"sent": len(s.sent)}, nil
}

func spec() addon.Spec {
	return addon.Spec{
		Manifest: addon.Manifest{ID: "t", Name: "T", Version: "1.0.0", APIVersion: addon.APIVersion,
			ConnectionTypes: []addon.ConnectionType{{ID: "s", Label: "S", Capabilities: []string{addon.CapSlides}}},
			Widgets:         []addon.WidgetDef{{Kind: "status", Type: "x", Label: "X", Sample: map[string]any{}}}},
		Open: map[string]addon.OpenFunc{"s": func(json.RawMessage) (any, error) { return &slides{}, nil }},
	}
}

func TestProtocol(t *testing.T) {
	h := addontest.Start(t, spec())
	if caps := h.Open("c", "s", nil); len(caps) != 1 || caps[0] != addon.CapSlides {
		t.Fatalf("caps %v", caps)
	}
	h.Call("c", "slides.do", map[string]string{"action": "next"}, nil)
	var st map[string]float64
	h.Call("c", "addon.state", nil, &st)
	if st["sent"] != 1 {
		t.Fatalf("state %v", st)
	}
	if err := h.TryCall("c", "switcher.cut", nil, nil); err == nil || !strings.Contains(err.Error(), "switcher") {
		t.Fatalf("slides answered a switcher call: %v", err)
	}
	if err := h.TryCall("nope", "slides.info", nil, nil); err == nil {
		t.Fatal("unknown connection answered")
	}
}

func TestManifestRules(t *testing.T) {
	m := spec().Manifest
	m.Name = strings.Repeat("x", 41)
	m.Description = strings.Repeat("y", 101)
	m.Widgets[0].Sample = nil
	err := m.Validate()
	for _, want := range []string{"name", "description", "sample"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want a %s error, got %v", want, err)
		}
	}
}

type pics struct{ slides }

func (p *pics) Resource(_ context.Context, path string) (string, []byte, error) {
	switch path {
	case "slides/1.png?w=640":
		return "image/png", []byte("\x89PNG fake"), nil
	case "huge.png":
		return "image/png", make([]byte, addon.MaxResource+1), nil
	}
	return "", nil, addon.ErrNotFound
}

func TestResource(t *testing.T) {
	sp := spec()
	sp.Open["p"] = func(json.RawMessage) (any, error) { return &pics{}, nil }
	sp.Manifest.ConnectionTypes = append(sp.Manifest.ConnectionTypes, addon.ConnectionType{ID: "p", Label: "P", Capabilities: []string{addon.CapSlides}})
	h := addontest.Start(t, sp)
	h.Open("c", "p", nil)
	ct, data, err := h.Resource("c", "slides/1.png?w=640")
	if err != nil || ct != "image/png" || string(data) != "\x89PNG fake" {
		t.Fatalf("resource: %q %q %v", ct, data, err)
	}
	var rpc *addon.RPCError
	if _, _, err := h.Resource("c", "missing.png"); !errors.As(err, &rpc) || rpc.Code != addon.CodeNotFound {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := h.Resource("c", "huge.png"); err == nil {
		t.Fatal("a resource over 4 MB was sent")
	}
	h.Open("s", "s", nil)
	if _, _, err := h.Resource("s", "x.png"); !errors.As(err, &rpc) || rpc.Code != addon.CodeNoResources {
		t.Fatalf("no resourcer: %v", err)
	}
}

// console is a v2.1 connection: per-connection options, state tailored to
// the asking widget, and fast resources with maxAge.
type console struct {
	slides
	got []map[string]any
}

func (c *console) AddonOptions(_ context.Context, name string) ([]addon.Option, error) {
	if name != "channels" {
		return nil, nil // fall back to Spec.Options
	}
	return []addon.Option{{Value: "ch1", Label: "Ch 1 · Speaker"}, {Value: "aux3", Label: "Aux 3 · Band"}}, nil
}

func (c *console) AddonStateFor(_ context.Context, settings map[string]any) (map[string]any, error) {
	c.got = append(c.got, settings)
	return map[string]any{"for": settings["channels"]}, nil
}

func (c *console) ResourceResult(_ context.Context, path string) (addon.ResourceResult, error) {
	if path == "meters" {
		return addon.ResourceResult{ContentType: "application/octet-stream", Data: []byte{1, 2}, MaxAge: addon.Age(0)}, nil
	}
	if path == "neg" {
		return addon.ResourceResult{Data: []byte{1}, MaxAge: addon.Age(-4)}, nil
	}
	return addon.ResourceResult{}, addon.ErrNotFound
}

func TestV21(t *testing.T) {
	sp := spec()
	sp.Options = map[string]func() []addon.Option{
		"channels": func() []addon.Option { return []addon.Option{{Value: "x", Label: "generic"}} },
		"ports":    func() []addon.Option { return []addon.Option{{Value: "p", Label: "Port"}} },
	}
	c := &console{}
	sp.Open = map[string]addon.OpenFunc{"s": func(json.RawMessage) (any, error) { return c, nil }}
	h := addontest.Start(t, sp)
	h.Open("c", "s", nil)

	// options: conn → the connection's list; without conn → Spec.Options;
	// a name the connection doesn't answer → Spec.Options too.
	if o := h.Options("channels", "c"); len(o) != 2 || o[0].Label != "Ch 1 · Speaker" {
		t.Fatalf("conn options %v", o)
	}
	if o := h.Options("channels", ""); len(o) != 1 || o[0].Value != "x" {
		t.Fatalf("plain options %v", o)
	}
	if o := h.Options("ports", "c"); len(o) != 1 || o[0].Value != "p" {
		t.Fatalf("fallback options %v", o)
	}
	if o := h.Options("channels", "gone"); len(o) != 1 {
		t.Fatalf("unknown conn options %v", o)
	}

	// state: settings reach AddonStateFor; a plain request goes to
	// AddonState (console has both, through slides).
	if st := h.State("c", map[string]any{"channels": []any{"ch1"}}); st["for"] == nil {
		t.Fatalf("state for widget %v", st)
	}
	if st := h.State("c", nil); st["sent"] == nil || len(c.got) != 1 {
		t.Fatalf("plain state %v, settings seen %v", st, c.got)
	}

	// resources: maxAge travels; negative becomes 0.
	r, err := h.ResourceResult("c", "meters")
	if err != nil || r.MaxAge == nil || *r.MaxAge != 0 || len(r.Data) != 2 {
		t.Fatalf("meters %+v %v", r, err)
	}
	r, err = h.ResourceResult("c", "neg")
	if err != nil || r.MaxAge == nil || *r.MaxAge != 0 || r.ContentType == "" {
		t.Fatalf("neg %+v %v", r, err)
	}
}

// both has AddonState and AddonStateFor: plain requests use AddonState.
type both struct {
	slides
	forCalls int
}

func (b *both) AddonStateFor(_ context.Context, s map[string]any) (map[string]any, error) {
	b.forCalls++
	return map[string]any{"tailored": true}, nil
}

func TestStateWithoutSettingsKeepsStater(t *testing.T) {
	sp := spec()
	b := &both{}
	sp.Open = map[string]addon.OpenFunc{"s": func(json.RawMessage) (any, error) { return b, nil }}
	h := addontest.Start(t, sp)
	h.Open("c", "s", nil)
	if st := h.State("c", nil); st["tailored"] != nil {
		t.Fatalf("plain state went to AddonStateFor: %v", st)
	}
	if st := h.State("c", map[string]any{}); st["tailored"] != true || b.forCalls != 1 {
		t.Fatalf("widget state %v", st)
	}
}

// onlyFor implements AddonStateFor alone: plain requests reach it with nil.
type onlyFor struct{ got []map[string]any }

func (o *onlyFor) Info() addon.PresentationInfo                       { return addon.PresentationInfo{} }
func (o *onlyFor) Actions() []string                                  { return nil }
func (o *onlyFor) Do(context.Context, string) (map[string]any, error) { return nil, nil }
func (o *onlyFor) AddonStateFor(_ context.Context, s map[string]any) (map[string]any, error) {
	o.got = append(o.got, s)
	return map[string]any{"n": len(o.got)}, nil
}

func TestStateForAlone(t *testing.T) {
	sp := spec()
	o := &onlyFor{}
	sp.Open = map[string]addon.OpenFunc{"s": func(json.RawMessage) (any, error) { return o, nil }}
	h := addontest.Start(t, sp)
	h.Open("c", "s", nil)
	h.State("c", nil)
	h.State("c", map[string]any{"a": 1.0})
	if len(o.got) != 2 || o.got[0] != nil || o.got[1]["a"] != 1.0 {
		t.Fatalf("seen %v", o.got)
	}
}
