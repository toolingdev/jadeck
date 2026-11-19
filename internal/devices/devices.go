// Package devices is the integration layer: generic interfaces for the
// gear around the camera, a driver registry, and the devices.json that
// says which drivers this install uses.
//
// Nothing here is venue-specific. Proclaim, the Behringer WING and vMix
// are just drivers (or driver configs). To support other gear, add a
// driver package that calls RegisterSwitcher / RegisterPresentation /
// RegisterMixer from init(), and blank-import it in cmd/ptz.
package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ------------------------------------------------------------ interfaces

// Input is one switcher source.
type Input struct {
	Number int    `json:"number"`
	Key    string `json:"key"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	State  string `json:"state"`
}

// SwitcherState is what a switcher reports. Fields a driver can't know
// stay zero.
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

// PresentationInfo describes how a presentation driver delivers
// actions (shown in the UI so operators know what's wired).
type PresentationInfo struct {
	Available bool           `json:"available"`
	Target    string         `json:"port"` // MIDI port, URL, ... ("port" for UI compatibility)
	Details   map[string]any `json:"details,omitempty"`
}

// Presentation is slide software (Proclaim, ProPresenter, ...).
// Actions are driver-config defined; the standard set is
// prev, next, black, logo, clear.
type Presentation interface {
	Info() PresentationInfo
	Actions() []string
	Do(ctx context.Context, action string) (detail map[string]any, err error)
}

// Mixer is an audio console. Channels are 1-based console channels.
type Mixer interface {
	Probe(ctx context.Context) (identity string, err error)
	MaxChannel() int
	GetMute(ctx context.Context, ch int) (bool, error)
	SetMute(ctx context.Context, ch int, muted bool) error
}

// Closer is optionally implemented by drivers holding OS resources.
type Closer interface{ Close() error }

// ---------------------------------------------------------------- errors

var (
	ErrNotConfigured = errors.New("not set up")
	ErrUnknownAction = errors.New("unknown action")
)

// ---------------------------------------------------------------- registry

type (
	SwitcherFactory     func(cfg json.RawMessage) (Switcher, error)
	PresentationFactory func(cfg json.RawMessage) (Presentation, error)
	MixerFactory        func(cfg json.RawMessage) (Mixer, error)
)

var (
	regMu         sync.Mutex
	switchers     = map[string]SwitcherFactory{}
	presentations = map[string]PresentationFactory{}
	mixers        = map[string]MixerFactory{}
)

func RegisterSwitcher(name string, f SwitcherFactory) { reg(switchers, name, f) }
func RegisterPresentation(name string, f PresentationFactory) {
	reg(presentations, name, f)
}
func RegisterMixer(name string, f MixerFactory) { reg(mixers, name, f) }

func reg[F any](m map[string]F, name string, f F) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := m[name]; dup {
		panic("devices: driver registered twice: " + name)
	}
	m[name] = f
}

// Drivers lists registered driver names per kind.
func Drivers() map[string][]string {
	regMu.Lock()
	defer regMu.Unlock()
	keys := func(n int, each func(func(string))) []string {
		out := make([]string, 0, n)
		each(func(k string) { out = append(out, k) })
		sort.Strings(out)
		return out
	}
	return map[string][]string{
		"switcher": keys(len(switchers), func(f func(string)) {
			for k := range switchers {
				f(k)
			}
		}),
		"presentation": keys(len(presentations), func(f func(string)) {
			for k := range presentations {
				f(k)
			}
		}),
		"mixer": keys(len(mixers), func(f func(string)) {
			for k := range mixers {
				f(k)
			}
		}),
	}
}

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

// File is devices.json.
type File struct {
	Switcher     *DeviceConfig `json:"switcher"`
	Presentation *DeviceConfig `json:"presentation"`
	Mixer        *DeviceConfig `json:"mixer"`
	Favorites    []Favorite    `json:"favorites"`
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
	return &f, nil
}

// ---------------------------------------------------------------- set

// Slot is a resolved favorite.
type Slot struct {
	Slot    int    `json:"slot"`
	Label   string `json:"label"`
	Channel *int   `json:"channel"`
}

// Set is the live devices of this install. Any member may be nil (not
// configured); its Err says why when a driver failed to start.
type Set struct {
	Switcher     Switcher
	Presentation Presentation
	Mixer        Mixer

	SwitcherCfg, PresentationCfg, MixerCfg DeviceConfig
	SwitcherErr, PresentationErr, MixerErr error

	Favorites []Slot
}

// Open builds the device set. A driver that fails to start is reported
// in the *Err field instead of taking the app down.
func Open(f *File) *Set {
	s := &Set{}
	if f == nil {
		f = &File{}
	}
	if c := f.Switcher; c != nil && c.Driver != "" {
		s.SwitcherCfg = *c
		if fac, ok := switchers[c.Driver]; !ok {
			s.SwitcherErr = fmt.Errorf("unknown switcher driver %q", c.Driver)
		} else {
			s.Switcher, s.SwitcherErr = fac(c.Raw)
		}
	}
	if c := f.Presentation; c != nil && c.Driver != "" {
		s.PresentationCfg = *c
		if fac, ok := presentations[c.Driver]; !ok {
			s.PresentationErr = fmt.Errorf("unknown presentation driver %q", c.Driver)
		} else {
			s.Presentation, s.PresentationErr = fac(c.Raw)
		}
	}
	if c := f.Mixer; c != nil && c.Driver != "" {
		s.MixerCfg = *c
		if fac, ok := mixers[c.Driver]; !ok {
			s.MixerErr = fmt.Errorf("unknown mixer driver %q", c.Driver)
		} else {
			s.Mixer, s.MixerErr = fac(c.Raw)
		}
	}
	for i, fav := range f.Favorites {
		sl := Slot{Slot: i + 1, Label: fav.Label}
		if sl.Label == "" {
			sl.Label = fmt.Sprintf("Fav %d", i+1)
		}
		if fav.Channel > 0 {
			ch := fav.Channel
			sl.Channel = &ch
		}
		s.Favorites = append(s.Favorites, sl)
	}
	return s
}

func (s *Set) Close() {
	for _, d := range []any{s.Switcher, s.Presentation, s.Mixer} {
		if c, ok := d.(Closer); ok {
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
