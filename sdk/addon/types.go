// Package addon is jadeck add-on SDK.
//
// An add-on teaches the controller one product (vMix, a mixing console,
// slide software...). It runs as its own process and talks to the
// controller over stdin/stdout (see protocol.go). An add-on is a Go
// program whose main calls Serve with a Spec:
//
//	func main() {
//		addon.Serve(addon.Spec{
//			Manifest: addon.Manifest{ID: "acme", Name: "Acme", Version: "1.0.0", ...},
//			Open: map[string]addon.OpenFunc{"acme": openAcme},
//		})
//	}
//
// A connection (what Open returns) implements one or more capability
// interfaces below. The controller's built-in widgets, macros and agent
// tools work with any connection that has the capability, so most
// add-ons never ship UI of their own.
package addon

import (
	"context"
	"encoding/json"
	"errors"
)

// APIVersion is the protocol version this SDK speaks.
const APIVersion = 1

// Capabilities a connection can have.
const (
	CapSwitcher = "switcher" // Switcher: inputs, preview, cut, fade, live/recording
	CapSlides   = "slides"   // Slides: prev / next / blank ... actions
	CapMixer    = "mixer"    // Mixer: channel mutes, always read back
	// CapAssistant marks a connection that people talk to (the AI
	// controller); it works through AddonAction / AddonState.
	CapAssistant = "assistant"
)

// ------------------------------------------------------------ switcher

// Input is one switcher source.
type Input struct {
	Number int    `json:"number"`
	Key    string `json:"key"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	State  string `json:"state"`
}

// SwitcherState is what a switcher reports. Fields it can't know stay zero.
type SwitcherState struct {
	Version     string  `json:"version"`
	Edition     string  `json:"edition"`
	Inputs      []Input `json:"inputs"`
	Preview     int     `json:"preview"`
	Active      int     `json:"active"`
	Streaming   bool    `json:"streaming"`
	Recording   bool    `json:"recording"`
	FadeToBlack bool    `json:"fadeToBlack"`
}

// Switcher is a video switcher (vMix, OBS, ATEM, ...).
type Switcher interface {
	State(ctx context.Context) (SwitcherState, error)
	Preview(ctx context.Context, input int) error
	Cut(ctx context.Context) error
	Fade(ctx context.Context, durationMs int) error
}

// -------------------------------------------------------------- slides

// PresentationInfo describes how slide actions are delivered (shown in
// the UI so operators know what's wired).
type PresentationInfo struct {
	Available bool           `json:"available"`
	Target    string         `json:"port"` // MIDI port, URL, ... ("port" for UI compatibility)
	Details   map[string]any `json:"details,omitempty"`
}

// Presentation is slide software (Proclaim, ProPresenter, ...). The
// standard actions are prev, next, black, logo, clear.
type Presentation interface {
	Info() PresentationInfo
	Actions() []string
	Do(ctx context.Context, action string) (detail map[string]any, err error)
}

// --------------------------------------------------------------- mixer

// Mixer is an audio console. Channels are 1-based console channels.
type Mixer interface {
	Probe(ctx context.Context) (identity string, err error)
	MaxChannel() int
	GetMute(ctx context.Context, ch int) (bool, error)
	SetMute(ctx context.Context, ch int, muted bool) error
}

// -------------------------------------------------------- add-on extras

// Stater reports product-specific state for the add-on's own widgets
// (e.g. Proclaim's service items). Optional.
type Stater interface {
	AddonState(ctx context.Context) (map[string]any, error)
}

// Actioner runs the add-on's own actions (Manifest.Actions). Optional.
type Actioner interface {
	AddonAction(ctx context.Context, name string, params json.RawMessage) (any, error)
}

// Closer is implemented by connections holding resources.
type Closer interface{ Close() error }

var (
	ErrNotConfigured = errors.New("not set up")
	ErrUnknownAction = errors.New("unknown action")
)

// Caps lists the capabilities a connection value implements.
func Caps(conn any) []string {
	var out []string
	if _, ok := conn.(Switcher); ok {
		out = append(out, CapSwitcher)
	}
	if _, ok := conn.(Presentation); ok {
		out = append(out, CapSlides)
	}
	if _, ok := conn.(Mixer); ok {
		out = append(out, CapMixer)
	}
	return out
}

// ControllerAccess is how a Controller add-on reaches the controller:
// its HTTP API and MCP endpoint (URL + "/mcp"), with Token sent as
// "Authorization: Bearer <token>". The token lasts while the connection
// is open and carries only the permissions picked for it.
type ControllerAccess struct {
	URL   string   `json:"url"`
	Token string   `json:"token"`
	Can   []string `json:"can"`
}

// ControllerFrom reads the access the controller put in a connection's
// config (nil when the add-on isn't a Controller add-on).
func ControllerFrom(config json.RawMessage) *ControllerAccess {
	var c struct {
		C *ControllerAccess `json:"_controller"`
	}
	json.Unmarshal(config, &c)
	return c.C
}
