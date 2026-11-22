// Package devices is the controller's view of the gear around the
// camera: connections (saved in devices.json), each opened through an
// add-on, and the capability interfaces they implement.
//
// Nothing here knows any product. vMix, the Behringer WING, Proclaim...
// are add-ons, separate projects that speak the add-on protocol
// (github.com/toolingdev/jadeck/sdk/addon).
package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// ------------------------------------------------------------ interfaces
//
// The capability types live in the add-on SDK (pkg/addon) so drivers,
// add-on programs and the controller share one definition.

type (
	Input            = addon.Input
	SwitcherState    = addon.SwitcherState
	Switcher         = addon.Switcher
	PresentationInfo = addon.PresentationInfo
	Presentation     = addon.Presentation
	Mixer            = addon.Mixer
	Closer           = addon.Closer
)

var (
	ErrNotConfigured = addon.ErrNotConfigured
	ErrUnknownAction = addon.ErrUnknownAction
)

// ---------------------------------------------------------------- config

// DeviceConfig selects a driver; every other key is passed to the
// driver as its own config.
type DeviceConfig struct {
	Driver string          `json:"driver"`
	Label  string          `json:"label"`
	Raw    json.RawMessage `json:"-"`
}

func (d *DeviceConfig) UnmarshalJSON(b []byte) error {
	var head struct {
		Driver string `json:"driver"`
		Label  string `json:"label"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	d.Driver, d.Label = head.Driver, head.Label
	d.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// MarshalJSON writes the full original object (driver, label, settings).
func (d DeviceConfig) MarshalJSON() ([]byte, error) {
	if len(d.Raw) > 0 {
		return d.Raw, nil
	}
	return json.Marshal(struct {
		Driver string `json:"driver"`
		Label  string `json:"label"`
	}{d.Driver, d.Label})
}

// Favorite is one mixer quick-mute slot on the dashboard.
type Favorite struct {
	Label   string `json:"label"`
	Channel int    `json:"channel"` // 0 = placeholder, not assigned
}

// Connection is one piece of gear this install talks to, through an
// add-on: "vMix — office PC", "Proclaim — sanctuary PC". Any number of
// each kind.
type Connection struct {
	ID     string          `json:"id"`
	Addon  string          `json:"addon"`
	Type   string          `json:"type"` // the add-on's connection type
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config,omitempty"`
	// Quick channels, for mixer connections: the channels shown on Mute
	// buttons and the Favorites widget, with the names people know.
	Favorites []Favorite `json:"favorites,omitempty"`
	// Which camera is on which input, for switcher connections: tally on
	// every dashboard uses it (camera id -> input number). Cameras not in
	// it are matched by name.
	CameraInputs map[string]int `json:"cameraInputs,omitempty"`
	Disabled     bool           `json:"disabled,omitempty"`
}

// File is devices.json. Version 1 had one switcher, one presentation and
// one mixer; LoadFile turns that into connections.
type File struct {
	Version     int          `json:"version"`
	Connections []Connection `json:"connections"`

	// version 1, read only
	Switcher     *DeviceConfig `json:"switcher,omitempty"`
	Presentation *DeviceConfig `json:"presentation,omitempty"`
	Mixer        *DeviceConfig `json:"mixer,omitempty"`
	Favorites    []Favorite    `json:"favorites,omitempty"`
}

// LegacyAddon maps a version-1 driver to the add-on and connection type
// that replaced it.
func LegacyAddon(kind, driver string) (addonID, typ string) {
	switch driver {
	case "vmix":
		return "vmix", "vmix"
	case "osc":
		return "behringer-wing", "osc"
	case "midi":
		return "midi", "midi"
	case "http":
		return "http-api", "http"
	case "sim":
		return "simulated", map[string]string{"switcher": "sim-switcher", "presentation": "sim-slides", "mixer": "sim-mixer"}[kind]
	}
	return driver, driver
}

// Migrate turns a version-1 file into connections (no-op for version 2).
func (f *File) Migrate() bool {
	if f.Version >= 2 {
		return false
	}
	add := func(kind string, d *DeviceConfig, favs []Favorite) {
		if d == nil || d.Driver == "" {
			return
		}
		a, t := LegacyAddon(kind, d.Driver)
		name := d.Label
		if name == "" {
			name = map[string]string{"switcher": "Video switcher", "presentation": "Slides", "mixer": "Audio mixer"}[kind]
		}
		var cfg map[string]any
		json.Unmarshal(d.Raw, &cfg)
		delete(cfg, "driver")
		delete(cfg, "label")
		raw, _ := json.Marshal(cfg)
		f.Connections = append(f.Connections, Connection{ID: NewID(f, a), Addon: a, Type: t, Name: name, Config: raw, Favorites: favs})
	}
	add("switcher", f.Switcher, nil)
	add("presentation", f.Presentation, nil)
	add("mixer", f.Mixer, f.Favorites)
	if f.Mixer == nil && len(f.Favorites) > 0 {
		// favorites without a mixer: keep them on a placeholder so nothing is lost
		f.Connections = append(f.Connections, Connection{ID: NewID(f, "mixer"), Addon: "simulated", Type: "sim-mixer",
			Name: "Audio mixer (not set up)", Favorites: f.Favorites, Disabled: true})
	}
	f.Version = 2
	f.Switcher, f.Presentation, f.Mixer, f.Favorites = nil, nil, nil, nil
	return true
}

// NewID makes a connection id unique within the file.
func NewID(f *File, base string) string {
	base = strings.ToLower(strings.TrimSpace(base))
	if base == "" {
		base = "conn"
	}
	id := base
	for n := 2; ; n++ {
		taken := false
		for _, c := range f.Connections {
			if c.ID == id {
				taken = true
			}
		}
		if !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

func LoadFile(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f.Migrate()
	return &f, nil
}

// ---------------------------------------------------------------- set

// Slot is a resolved favorite.
type Slot struct {
	Slot    int    `json:"slot"`
	Label   string `json:"label"`
	Channel *int   `json:"channel"`
}

// Live is an opened connection.
type Live struct {
	Connection
	Caps  []string // capabilities it has
	Impl  any      // implements the capability interfaces
	Err   error    // why it couldn't open
	Slots []Slot   // resolved favorites (mixers)
}

func (l *Live) Has(cap string) bool {
	for _, c := range l.Caps {
		if c == cap {
			return true
		}
	}
	return false
}

// Opener opens one connection (through its add-on).
type Opener func(c Connection) (impl any, caps []string, err error)

// Set is the live connections of this install. Switcher / Presentation /
// Mixer are the defaults (the first connection with that capability) so
// widgets and macros that don't name a connection keep working.
type Set struct {
	Conns []*Live

	Switcher     Switcher
	Presentation Presentation
	Mixer        Mixer

	SwitcherCfg, PresentationCfg, MixerCfg DeviceConfig
	SwitcherErr, PresentationErr, MixerErr error

	Favorites []Slot // the default mixer's quick channels

	CameraInputs map[string]int // the default switcher's camera -> input table
}

// Open builds the set. A connection that fails to open is reported in
// its Err instead of taking the app down.
func Open(f *File, open Opener) *Set {
	return Reopen(nil, f, open)
}

// sameOpen reports whether two connections open the same way (only their
// name, quick channels or camera inputs differ).
func sameOpen(a, b Connection) bool {
	return a.ID == b.ID && a.Addon == b.Addon && a.Type == b.Type && a.Disabled == b.Disabled && bytes.Equal(a.Config, b.Config)
}

// Reopen builds the set for f, keeping every open connection of old that
// is unchanged (so adding or editing one connection doesn't restart the
// others mid-service). Connections that changed or went away are closed
// first (a MIDI port can only be open once), then the new ones open.
// old's leftovers are closed; old itself must not be used afterwards.
func Reopen(old *Set, f *File, open Opener) *Set {
	s := &Set{}
	if f == nil {
		f = &File{Version: 2}
	}
	f.Migrate()
	keep := map[string]*Live{}
	if old != nil {
		want := map[string]Connection{}
		for _, c := range f.Connections {
			want[c.ID] = c
		}
		for _, l := range old.Conns {
			if c, ok := want[l.ID]; ok && l.Impl != nil && sameOpen(l.Connection, c) {
				keep[l.ID] = l
				continue
			}
			if c, ok := l.Impl.(Closer); ok {
				c.Close()
			}
		}
	}
	for _, c := range f.Connections {
		l := &Live{Connection: c}
		if k := keep[c.ID]; k != nil {
			l.Impl, l.Caps, l.Err = k.Impl, k.Caps, k.Err
		}
		for i, fav := range c.Favorites {
			sl := Slot{Slot: i + 1, Label: fav.Label}
			if sl.Label == "" {
				sl.Label = fmt.Sprintf("Fav %d", i+1)
			}
			if fav.Channel > 0 {
				ch := fav.Channel
				sl.Channel = &ch
			}
			l.Slots = append(l.Slots, sl)
		}
		switch {
		case keep[c.ID] != nil:
			// still open from before
		case c.Disabled:
			l.Err = fmt.Errorf("turned off")
		case open == nil:
			l.Err = fmt.Errorf("no add-on host")
		default:
			l.Impl, l.Caps, l.Err = open(c)
		}
		s.Conns = append(s.Conns, l)
	}
	s.pickDefaults()
	return s
}

// expected capability per kind (for defaults and errors)
var kindCap = map[string]string{"switcher": addon.CapSwitcher, "presentation": addon.CapSlides, "mixer": addon.CapMixer}

// declaredCap is filled by the host: connection type → capabilities from
// the add-on's manifest, so a connection that failed to open still
// counts as "the switcher" (and shows its error) instead of vanishing.
var DeclaredCaps func(c Connection) []string

func (s *Set) pickDefaults() {
	cfg := func(l *Live) DeviceConfig {
		raw, _ := json.Marshal(map[string]any{"driver": l.Type, "label": l.Name})
		return DeviceConfig{Driver: l.Type, Label: l.Name, Raw: raw}
	}
	for _, kind := range []string{"switcher", "presentation", "mixer"} {
		capName := kindCap[kind]
		var pick *Live
		for _, l := range s.Conns {
			if l.Disabled {
				continue
			}
			has := l.Has(capName)
			if !has && l.Impl == nil && DeclaredCaps != nil {
				for _, c := range DeclaredCaps(l.Connection) {
					has = has || c == capName
				}
			}
			if has {
				pick = l
				break
			}
		}
		if pick == nil {
			continue
		}
		switch kind {
		case "switcher":
			s.SwitcherCfg, s.SwitcherErr = cfg(pick), pick.Err
			s.Switcher, _ = pick.Impl.(Switcher)
			s.CameraInputs = pick.CameraInputs
		case "presentation":
			s.PresentationCfg, s.PresentationErr = cfg(pick), pick.Err
			s.Presentation, _ = pick.Impl.(Presentation)
		case "mixer":
			s.MixerCfg, s.MixerErr = cfg(pick), pick.Err
			s.Mixer, _ = pick.Impl.(Mixer)
			s.Favorites = pick.Slots
		}
	}
}

// Get finds a live connection by id.
func (s *Set) Get(id string) *Live {
	for _, l := range s.Conns {
		if l.ID == id {
			return l
		}
	}
	return nil
}

// Of returns the connections with a capability.
func (s *Set) Of(cap string) []*Live {
	var out []*Live
	for _, l := range s.Conns {
		if l.Has(cap) || (l.Impl == nil && DeclaredCaps != nil && contains(DeclaredCaps(l.Connection), cap)) {
			out = append(out, l)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Scoped returns a view of the set whose defaults are the given
// connection (for API calls that name one with ?conn=). Unknown or
// empty id returns the set itself.
func (s *Set) Scoped(id string) (*Set, error) {
	if id == "" {
		return s, nil
	}
	l := s.Get(id)
	if l == nil {
		return nil, fmt.Errorf("no connection %q", id)
	}
	v := &Set{Conns: []*Live{l}}
	v.pickDefaults()
	return v, nil
}

func (s *Set) Close() {
	for _, l := range s.Conns {
		if c, ok := l.Impl.(Closer); ok {
			c.Close()
		}
	}
}

// FavoriteByName resolves a favorite label (case-insensitive).
func (s *Set) FavoriteByName(name string) (Slot, bool) {
	for _, f := range s.Favorites {
		if strings.EqualFold(strings.TrimSpace(f.Label), strings.TrimSpace(name)) {
			return f, true
		}
	}
	return Slot{}, false
}

// ------------------------------------------------------- verified mutes

// MuteChannel sets a console channel's mute and REQUIRES the read-back
// to match: macro steps and dashboard buttons move live audio, and
// "we sent it" is not "it happened".
func (s *Set) MuteChannel(ctx context.Context, ch int, muted bool) (bool, error) {
	if s.Mixer == nil {
		return false, fmt.Errorf("mixer is %w", ErrNotConfigured)
	}
	if err := s.Mixer.SetMute(ctx, ch, muted); err != nil {
		return false, err
	}
	// UDP consoles apply asynchronously; allow a few quick read-backs.
	var back bool
	var err error
	for i := 0; i < 5; i++ {
		back, err = s.Mixer.GetMute(ctx, ch)
		if err == nil && back == muted {
			return back, nil
		}
		select {
		case <-ctx.Done():
			return back, ctx.Err()
		case <-time.After(40 * time.Millisecond):
		}
	}
	if err != nil {
		return false, err
	}
	state := func(m bool) string {
		if m {
			return "muted"
		}
		return "live"
	}
	return back, fmt.Errorf("channel %d read back %s after being set %s — the console did not take the change",
		ch, state(back), state(muted))
}

// MuteSlot sets (or, with muted nil, toggles) a favorite slot, always
// verified by read-back.
func (s *Set) MuteSlot(ctx context.Context, slot int, muted *bool) (Slot, bool, error) {
	var sl *Slot
	for i := range s.Favorites {
		if s.Favorites[i].Slot == slot {
			sl = &s.Favorites[i]
		}
	}
	if sl == nil {
		return Slot{}, false, fmt.Errorf("no favorite slot %d: %w", slot, ErrUnassigned)
	}
	if sl.Channel == nil {
		return *sl, false, fmt.Errorf("%s has no console channel yet: %w", sl.Label, ErrUnassigned)
	}
	if s.Mixer == nil {
		return *sl, false, fmt.Errorf("mixer is %w", ErrNotConfigured)
	}
	want := false
	if muted != nil {
		want = *muted
	} else {
		cur, err := s.Mixer.GetMute(ctx, *sl.Channel)
		if err != nil {
			return *sl, false, err
		}
		want = !cur
	}
	back, err := s.MuteChannel(ctx, *sl.Channel, want)
	return *sl, back, err
}

// ErrUnassigned: a favorite slot without a console channel (a setup
// fact, not a failure).
var ErrUnassigned = errors.New("unassigned")
