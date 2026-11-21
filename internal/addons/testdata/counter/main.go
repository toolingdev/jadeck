// A test add-on for the host tests: a switcher whose program input is
// counted up by every cut, and a "crash" action that exits.
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/toolingdev/jadeck/sdk/addon"
)

var Version = "1.0.0"

type sw struct{ active int }

func (s *sw) State(context.Context) (addon.SwitcherState, error) {
	return addon.SwitcherState{Active: s.active, Inputs: []addon.Input{{Number: 1, Title: "One"}}}, nil
}
func (s *sw) Preview(context.Context, int) error { return nil }
func (s *sw) Cut(context.Context) error          { s.active++; return nil }
func (s *sw) Fade(context.Context, int) error    { return nil }
func (s *sw) AddonAction(_ context.Context, name string, _ json.RawMessage) (any, error) {
	if name == "crash" {
		os.Exit(3)
	}
	return nil, addon.ErrUnknownAction
}

func (s *sw) Resource(_ context.Context, path string) (string, []byte, error) {
	if path == "pic.png?w=2" {
		return "image/png", []byte("PNG!"), nil
	}
	return "", nil, addon.ErrNotFound
}

// v2.1: a fast resource that must not be cached, options for this
// connection, and state for one widget.
func (s *sw) ResourceResult(ctx context.Context, path string) (addon.ResourceResult, error) {
	if path == "meters" {
		return addon.ResourceResult{ContentType: "application/octet-stream", Data: []byte{byte(s.active)}, MaxAge: addon.Age(0)}, nil
	}
	ct, b, err := s.Resource(ctx, path)
	return addon.ResourceResult{ContentType: ct, Data: b}, err
}

func (s *sw) AddonOptions(_ context.Context, name string) ([]addon.Option, error) {
	if name == "inputs" {
		return []addon.Option{{Value: "1", Label: "In 1 · One"}}, nil
	}
	return nil, nil
}

func (s *sw) AddonState(context.Context) (map[string]any, error) {
	return map[string]any{"active": s.active}, nil
}

func (s *sw) AddonStateFor(_ context.Context, settings map[string]any) (map[string]any, error) {
	return map[string]any{"active": s.active, "settings": settings}, nil
}

func main() {
	addon.Serve(addon.Spec{
		Manifest: addon.Manifest{ID: "counter", Name: "Counter", Version: Version, Publisher: "tests", APIVersion: addon.APIVersion,
			ConnectionTypes: []addon.ConnectionType{{ID: "sw", Label: "Counter", Capabilities: []string{addon.CapSwitcher}, Fields: []addon.Field{}}}},
		Open:    map[string]addon.OpenFunc{"sw": func(json.RawMessage) (any, error) { return &sw{}, nil }},
		Options: map[string]func() []addon.Option{"inputs": func() []addon.Option { return []addon.Option{{Value: "g", Label: "generic"}} }},
	})
}
