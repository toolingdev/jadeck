package addontest_test

import (
	"context"
	"encoding/json"
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
