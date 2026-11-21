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

// APIVersion is the protocol version this SDK speaks. 2 added the
// shared widget shapes, frame widgets, ui/ files and addon.resource; a
// controller that speaks 2 still runs apiVersion 1 add-ons unchanged.
const APIVersion = 2

// Capabilities a connection can have.
const (
	CapSwitcher = "switcher" // Switcher: inputs, preview, cut, fade, live/recording
	CapSlides   = "slides"   // Slides: prev / next / blank ... actions
	CapMixer    = "mixer"    // Mixer: channel mutes, always read back
	// CapAssistant marks a connection that people talk to (the AI
	// controller); it works through AddonAction / AddonState.
	CapAssistant = "assistant"
	// CapTool (apiVersion 2) marks a general add-on connection: it adds
	// widgets and actions (AddonAction / AddonState) but isn't a
	// switcher, slides, mixer or assistant. Controllers older than
	// apiVersion 2 don't know it.
	CapTool = "tool"
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

// SettingsStater is a Stater that can tailor its state to one widget.
// When a dashboard widget with Fields asks for state, the controller
// sends that widget's settings (field defaults filled in, only the keys
// WidgetDef.Fields declares) and AddonStateFor is called instead of
// AddonState; settings is nil when the request names no widget. A
// connection may implement this alone, without Stater. Optional (v2.1).
type SettingsStater interface {
	AddonStateFor(ctx context.Context, settings map[string]any) (map[string]any, error)
}

// StateParams is addon.state's params (v2.1; absent before).
type StateParams struct {
	Settings map[string]any `json:"settings"` // {} for a widget with no settings yet; null or absent: no widget
}

// Optioner answers live option lists for one connection (Field.OptionsFrom
// on a widget field, e.g. a console's channel names). The controller asks
// it when a widget's settings panel names the widget's connection; nil
// options (or no Optioner) fall back to Spec.Options. Optional (v2.1).
type Optioner interface {
	AddonOptions(ctx context.Context, name string) ([]Option, error)
}

// OptionsParams is the options request's params. Conn is empty for a
// connection settings form (Spec.Options answers) and names the
// connection for a widget field (its Optioner answers first).
type OptionsParams struct {
	Name string `json:"name"`
	Conn string `json:"conn,omitempty"`
}

// Actioner runs the add-on's own actions (Manifest.Actions). Optional.
type Actioner interface {
	AddonAction(ctx context.Context, name string, params json.RawMessage) (any, error)
}

// Resourcer serves pictures (and other files) the add-on's state points
// at: a state value "res:slides/12/3.png?w=640" is fetched as
// Resource(ctx, "slides/12/3.png?w=640"). The path is whatever the add-on
// wrote after "res:" (query included). At most MaxResource bytes; return
// ErrNotFound for a path it doesn't have. Optional (apiVersion 2).
type Resourcer interface {
	Resource(ctx context.Context, path string) (contentType string, data []byte, err error)
}

// ResultResourcer is a Resourcer that also says how long the controller
// may cache the answer (ResourceResult.MaxAge), e.g. meters that change
// many times a second. When a connection implements it, it is used
// instead of Resource. Optional (v2.1).
type ResultResourcer interface {
	ResourceResult(ctx context.Context, path string) (ResourceResult, error)
}

// MaxResource is the largest resource an add-on may return.
const MaxResource = 4 << 20

// ResourceParams is addon.resource's params.
type ResourceParams struct {
	Path string `json:"path"`
}

// ResourceResult is addon.resource's result; Data travels as base64.
// MaxAge is how many seconds the controller may cache it: nil keeps the
// default (DefaultMaxAge, 2 s), 0 means never cache (the controller still
// answers requests for the same path within 50 ms with one call).
type ResourceResult struct {
	ContentType string `json:"contentType"`
	Data        []byte `json:"data"`
	MaxAge      *int   `json:"maxAge,omitempty"`
}

// DefaultMaxAge is how long (seconds) a resource without MaxAge is cached.
const DefaultMaxAge = 2

// MaxMaxAge is the longest MaxAge the controller honours (seconds).
const MaxMaxAge = 3600

// Age is a helper for ResourceResult.MaxAge.
func Age(seconds int) *int { return &seconds }

// Closer is implemented by connections holding resources.
type Closer interface{ Close() error }

var (
	ErrNotConfigured = errors.New("not set up")
	ErrUnknownAction = errors.New("unknown action")
	// ErrNoResources: the connection doesn't serve resources (no Resourcer).
	ErrNoResources = errors.New("this connection has no resources")
	// ErrNotFound: a resource the add-on doesn't have.
	ErrNotFound = errors.New("not found")
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
