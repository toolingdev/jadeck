package devices

import (
	"sort"
	"sync"
)

// Drivers describe their own settings so the Settings screen can build
// a form for any driver, including ones other people add later.

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field is one setting. Types: text, number, select, bool, notes (a map
// of action → MIDI note), json (free-form object), password.
type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Default     any      `json:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Advanced    bool     `json:"advanced,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Options     []Option `json:"options,omitempty"`
	// OptionsFrom names a live option source (e.g. "midi.ports"), listed
	// by the server when the form opens; free text stays allowed.
	OptionsFrom string   `json:"optionsFrom,omitempty"`
	Keys        []string `json:"keys,omitempty"` // notes: which actions
}

type Meta struct {
	Kind        string  `json:"kind"`
	Driver      string  `json:"driver"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
}

func Num(v float64) *float64 { return &v }

var (
	metaMu  sync.Mutex
	metas   = map[string]Meta{} // kind/driver
	options = map[string]func() []Option{}
)

// Describe registers a driver's settings form.
func Describe(m Meta) {
	metaMu.Lock()
	defer metaMu.Unlock()
	metas[m.Kind+"/"+m.Driver] = m
}

// RegisterOptions registers a live option source for select fields.
func RegisterOptions(name string, f func() []Option) {
	metaMu.Lock()
	defer metaMu.Unlock()
	options[name] = f
}

// Metas lists every described driver, sim last.
func Metas() []Meta {
	metaMu.Lock()
	defer metaMu.Unlock()
	out := make([]Meta, 0, len(metas))
	for _, m := range metas {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if (out[i].Driver == "sim") != (out[j].Driver == "sim") {
			return out[j].Driver == "sim"
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// Options returns a live option list (nil if unknown).
func Options(name string) []Option {
	metaMu.Lock()
	f := options[name]
	metaMu.Unlock()
	if f == nil {
		return nil
	}
	return f()
}
