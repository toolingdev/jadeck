// Package macros stores and runs macros: named, ordered steps across
// every device kind. Authoring contract: MACROS.md.
//
// Step types:
//
//	{"type": "mixer_mute", "channel": 13, "muted": true}
//	{"type": "mixer_mute", "channelName": "MAIN", "muted": false}   (favorite label)
//	{"type": "switcher", "action": "preview", "input": 2}
//	{"type": "switcher", "action": "cut"}
//	{"type": "switcher", "action": "fade", "durationMs": 1000}
//	{"type": "slides", "action": "next"}
//	{"type": "camera_preset", "preset": "Pulpit"}                   (preset name)
//	{"type": "delay", "ms": 500}
//
// "wing_mute" (the Python build's name) is accepted and stored as
// mixer_mute, so existing macros keep working.
//
// SAFETY, because steps move live audio and video: a run is
// sequential; every mixer step is READ BACK and only counts if it
// matches; any failure, per-step timeout or the run deadline STOPS the
// macro at that step. Nothing is queued or retried silently, and only
// one macro runs at a time.
package macros

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MaxNameLen = 60
	MaxDescLen = 300
	MaxSteps   = 64
	MaxDelayMs = 60_000
	MaxFadeMs  = 10_000
)

var (
	ErrValidation = errors.New("invalid macro")
	ErrConflict   = errors.New("conflict")
	ErrNotFound   = errors.New("macro not found")
)

func verr(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrValidation}, a...)...)
}

// Macro is the stored document.
type Macro struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Steps       []Step         `json:"steps"`
	Sort        int            `json:"sort"`
	LastRun     map[string]any `json:"lastRun"`
	CreatedAt   float64        `json:"createdAt"`
	UpdatedAt   float64        `json:"updatedAt"`
}

// Step is one normalized step (fields unused by its type are omitted).
type Step struct {
	Type        string `json:"type"`
	Channel     int    `json:"channel,omitempty"`
	ChannelName string `json:"channelName,omitempty"`
	Muted       *bool  `json:"muted,omitempty"`
	Action      string `json:"action,omitempty"`
	Input       int    `json:"input,omitempty"`
	DurationMs  int    `json:"durationMs,omitempty"`
	Preset      string `json:"preset,omitempty"`
	Ms          *int   `json:"ms,omitempty"`
}

// Env is what validation needs to resolve names.
type Env struct {
	// Favorite resolves a mixer favorite label -> channel (0 = unassigned).
	Favorite func(name string) (label string, channel int, ok bool)
	// FavoriteLabels lists labels for error messages.
	FavoriteLabels func() []string
	// MaxChannel is the mixer's top channel (0 = no mixer: 1..64 allowed).
	MaxChannel int
	// PresetExists checks a camera preset name.
	PresetExists func(name string) bool
}

var allowed = map[string][]string{
	"mixer_mute":    {"type", "channel", "channelName", "muted"},
	"switcher":      {"type", "action", "input", "durationMs"},
	"slides":        {"type", "action"},
	"camera_preset": {"type", "preset"},
	"delay":         {"type", "ms"},
}

func isInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

// Validate checks one raw macro document (decoded JSON) and returns its
// normalized form, with step-numbered error messages.
func Validate(raw any, env Env) (*Macro, error) {
	doc, ok := raw.(map[string]any)
	if !ok {
		return nil, verr("a macro must be a JSON object")
	}
	for k := range doc {
		if k != "name" && k != "description" && k != "steps" {
			return nil, verr("unknown macro field %q (allowed: description, name, steps)", k)
		}
	}
	name, _ := doc["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, verr("name is required (a non-empty string)")
	}
	if len([]rune(name)) > MaxNameLen {
		return nil, verr("name is %d characters; the limit is %d", len([]rune(name)), MaxNameLen)
	}
	desc := ""
	if d, present := doc["description"]; present && d != nil {
		s, ok := d.(string)
		if !ok {
			return nil, verr("description must be a string")
		}
		desc = s
	}
	if len([]rune(desc)) > MaxDescLen {
		return nil, verr("description is %d characters; the limit is %d", len([]rune(desc)), MaxDescLen)
	}
	steps, ok := doc["steps"].([]any)
	if !ok || len(steps) == 0 {
		return nil, verr("steps is required (a non-empty list of steps)")
	}
	if len(steps) > MaxSteps {
		return nil, verr("%d steps; the limit is %d", len(steps), MaxSteps)
	}
	m := &Macro{Name: name, Description: desc}
	for i, s := range steps {
		st, err := validateStep(i+1, s, env)
		if err != nil {
			return nil, err
		}
		m.Steps = append(m.Steps, st)
	}
	return m, nil
}

func validateStep(n int, raw any, env Env) (Step, error) {
	s, ok := raw.(map[string]any)
	if !ok {
		return Step{}, verr("step %d: a step must be an object", n)
	}
	typ, _ := s["type"].(string)
	if typ == "wing_mute" {
		typ = "mixer_mute"
	}
	fields, known := allowed[typ]
	if !known {
		return Step{}, verr("step %d: unknown step type %q (types: camera_preset, delay, mixer_mute, slides, switcher)", n, s["type"])
	}
	for k := range s {
		found := false
		for _, f := range fields {
			found = found || f == k
		}
		if !found {
			return Step{}, verr("step %d: unknown field %q for a %s step", n, k, typ)
		}
	}
	out := Step{Type: typ}
	switch typ {
	case "mixer_mute":
		mv, ok := s["muted"].(bool)
		if !ok {
			return Step{}, verr("step %d: mixer_mute needs \"muted\" (true or false)", n)
		}
		out.Muted = &mv
		maxCh := env.MaxChannel
		if maxCh == 0 {
			maxCh = 64
		}
		ch := 0
		if c, present := s["channel"]; present {
			v, ok := isInt(c)
			if !ok {
				return Step{}, verr("step %d: \"channel\" must be an integer", n)
			}
			if v < 1 || v > maxCh {
				return Step{}, verr("step %d: channel %d is outside 1..%d", n, v, maxCh)
			}
			ch = v
		}
		if cn, present := s["channelName"]; present {
			name, ok := cn.(string)
			if !ok {
				return Step{}, verr("step %d: \"channelName\" must be a string", n)
			}
			if env.Favorite == nil {
				return Step{}, verr("step %d: no mixer favorites are configured", n)
			}
			label, fch, ok := env.Favorite(name)
			if !ok {
				labels := ""
				if env.FavoriteLabels != nil {
					labels = strings.Join(env.FavoriteLabels(), ", ")
				}
				return Step{}, verr("step %d: unknown favorite channel name %q — the favorites are: %s", n, name, labels)
			}
			if fch == 0 {
				return Step{}, verr("step %d: favorite %q is unassigned (it has no console channel yet)", n, label)
			}
			if ch != 0 && ch != fch {
				return Step{}, verr("step %d: channel %d disagrees with channelName %q, which is channel %d", n, ch, name, fch)
			}
			ch, out.ChannelName = fch, label
		}
		if ch == 0 {
			return Step{}, verr("step %d: mixer_mute needs a \"channel\" number or a \"channelName\" favorite", n)
		}
		out.Channel = ch
	case "switcher":
		a, _ := s["action"].(string)
		out.Action = a
		switch a {
		case "preview":
			v, ok := isInt(s["input"])
			if !ok || v < 1 || v > 999 {
				return Step{}, verr("step %d: switcher preview needs \"input\" (1..999)", n)
			}
			out.Input = v
		case "cut":
		case "fade":
			out.DurationMs = 1000
			if d, present := s["durationMs"]; present {
				v, ok := isInt(d)
				if !ok || v < 0 || v > MaxFadeMs {
					return Step{}, verr("step %d: durationMs must be 0..%d", n, MaxFadeMs)
				}
				out.DurationMs = v
			}
		default:
			return Step{}, verr("step %d: switcher action must be preview, cut or fade", n)
		}
	case "slides":
		a, _ := s["action"].(string)
		if a == "" {
			return Step{}, verr("step %d: slides needs \"action\" (e.g. next, prev, black)", n)
		}
		out.Action = a
	case "camera_preset":
		p, _ := s["preset"].(string)
		p = strings.TrimSpace(p)
		if p == "" {
			return Step{}, verr("step %d: camera_preset needs \"preset\" (a preset name)", n)
		}
		if env.PresetExists != nil && !env.PresetExists(p) {
			return Step{}, verr("step %d: no camera preset named %q", n, p)
		}
		out.Preset = p
	case "delay":
		v, ok := isInt(s["ms"])
		if !ok {
			return Step{}, verr("step %d: delay needs \"ms\" (an integer number of milliseconds)", n)
		}
		if v < 0 || v > MaxDelayMs {
			return Step{}, verr("step %d: delay %d ms is outside 0..%d", n, v, MaxDelayMs)
		}
		out.Ms = &v
	}
	return out, nil
}

// Describe renders one step in plain words (run logs and the builder).
func Describe(s Step) string {
	switch s.Type {
	case "delay":
		ms := *s.Ms
		if ms >= 1000 {
			return fmt.Sprintf("Wait %g s", float64(ms)/1000)
		}
		return fmt.Sprintf("Wait %d ms", ms)
	case "mixer_mute":
		verb := "Unmute"
		if *s.Muted {
			verb = "Mute"
		}
		if s.ChannelName != "" {
			return fmt.Sprintf("%s %s · Ch %d", verb, s.ChannelName, s.Channel)
		}
		return fmt.Sprintf("%s Ch %d", verb, s.Channel)
	case "switcher":
		switch s.Action {
		case "preview":
			return fmt.Sprintf("Switcher: preview input %d", s.Input)
		case "fade":
			return fmt.Sprintf("Switcher: fade (%d ms)", s.DurationMs)
		}
		return "Switcher: cut"
	case "slides":
		return "Slides: " + s.Action
	case "camera_preset":
		return "Camera: recall " + s.Preset
	}
	return s.Type
}

// ExtractImport accepts one macro, a list, or {"macros": [...],
// "validateOnly": bool}.
func ExtractImport(payload any) ([]any, bool, error) {
	switch p := payload.(type) {
	case []any:
		return p, false, nil
	case map[string]any:
		vo, _ := p["validateOnly"].(bool)
		if items, present := p["macros"]; present {
			l, ok := items.([]any)
			if !ok {
				return nil, false, verr("\"macros\" must be a list of macro objects")
			}
			return l, vo, nil
		}
		if _, ok := p["steps"]; ok {
			delete(p, "validateOnly")
			return []any{p}, vo, nil
		}
		if _, ok := p["name"]; ok {
			delete(p, "validateOnly")
			return []any{p}, vo, nil
		}
	}
	return nil, false, verr("import body must be a macro object, a list of macro objects, or {\"macros\": [...]}")
}
