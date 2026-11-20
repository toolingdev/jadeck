// A test add-on for the host tests: a switcher whose program input is
// counted up by every cut, and a "crash" action that exits.
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/toolingdev/ptz-controller/sdk/addon"
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

func main() {
	addon.Serve(addon.Spec{
		Manifest: addon.Manifest{ID: "counter", Name: "Counter", Version: Version, Publisher: "tests", APIVersion: addon.APIVersion,
			ConnectionTypes: []addon.ConnectionType{{ID: "sw", Label: "Counter", Capabilities: []string{addon.CapSwitcher}, Fields: []addon.Field{}}}},
		Open: map[string]addon.OpenFunc{"sw": func(json.RawMessage) (any, error) { return &sw{}, nil }},
	})
}
