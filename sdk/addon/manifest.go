package addon

import (
	"fmt"
	"regexp"
	"strings"
)

// Manifest says who an add-on is and everything it adds. It is shipped
// as addon.json next to the add-on program, so the controller can show
// it (with previews) without starting the add-on.
type Manifest struct {
	ID          string `json:"id"`          // lowercase, a-z 0-9 -
	Name        string `json:"name"`        // ≤ 40 characters
	Version     string `json:"version"`     // semver, e.g. 1.2.0
	Publisher   string `json:"publisher"`   // ≤ 40 characters
	Description string `json:"description"` // ≤ 100 characters
	APIVersion  int    `json:"apiVersion"`
	Generic     bool   `json:"generic,omitempty"` // for gear with no add-on of its own (MIDI, HTTP, ...)
	// Repo is the GitHub repository (owner/name) whose releases carry
	// <id>.ptzaddon; the controller checks it for updates.
	Repo string `json:"repo,omitempty"`

	// Controller: this add-on drives the controller itself (e.g. an AI
	// assistant). Each of its connections gets a token for the
	// controller's API and MCP endpoint, limited to the permissions
	// picked in the connection's "permissions" field (type scopes); see
	// ControllerFrom.
	Controller bool `json:"controller,omitempty"`

	// Reaches says, in plain words, what it talks to ("the vMix PCs you
	// add as connections"). Shown under "It can".
	Reaches string `json:"reaches"`

	ConnectionTypes []ConnectionType `json:"connectionTypes"`
	Actions         []ActionDef      `json:"actions,omitempty"`
	Widgets         []WidgetDef      `json:"widgets,omitempty"`
	Changelog       string           `json:"changelog,omitempty"` // what's new in this version
}

// ConnectionType is one kind of thing you can add under Settings →
// Connections ("vMix", "Proclaim").
type ConnectionType struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
	Fields       []Field  `json:"fields"`
	// Sample values for the form preview, and the "Test" line it shows.
	Sample     map[string]any `json:"sample,omitempty"`
	SampleTest string         `json:"sampleTest,omitempty"`
}

// ActionDef is an add-on action: a button, a macro step and an agent
// tool at once.
type ActionDef struct {
	ID          string  `json:"id"` // snake_case
	Label       string  `json:"label"`
	Description string  `json:"description,omitempty"`
	Params      []Field `json:"params,omitempty"`
	MacroStep   bool    `json:"macroStep,omitempty"`
}

// WidgetDef is a widget the add-on brings, or a built-in widget it powers
// (Kind "core": e.g. "slides.prevnext" works with any slides connection).
// Sample is what the preview shows: for core widgets the status the
// widget would read, for declarative widgets the add-on state.
type WidgetDef struct {
	Kind        string         `json:"kind"` // core | list | status
	Type        string         `json:"type"` // core: the built-in widget type; else the add-on's widget id
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	Bind        map[string]any `json:"bind,omitempty"`     // declarative: which state keys / action to use
	Settings    map[string]any `json:"settings,omitempty"` // core: widget settings for the preview
	Sample      map[string]any `json:"sample"`
}

// Option is one choice of a select field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field is one setting. Types: text, number, select, bool, password,
// notes (action → MIDI note), json (free-form object), scopes (which
// controller permissions a Controller add-on's connection gets),
// textarea (longer text).
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
	// OptionsFrom names a live option source the add-on answers
	// (e.g. "midi.ports"); free text stays allowed.
	OptionsFrom string   `json:"optionsFrom,omitempty"`
	Keys        []string `json:"keys,omitempty"` // notes: which actions
}

// Num is a helper for Field.Min / Max.
func Num(v float64) *float64 { return &v }

var (
	idRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	actionRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
)

// Validate checks the manifest against the rules the Add-ons screen
// depends on (short names, previews for every widget, ...).
func (m Manifest) Validate() error {
	var errs []string
	bad := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if !idRe.MatchString(m.ID) {
		bad("id %q: use a-z, 0-9 and -, at most 40", m.ID)
	}
	if m.Name == "" || len([]rune(m.Name)) > 40 {
		bad("name must be 1-40 characters")
	}
	if len([]rune(m.Publisher)) > 40 {
		bad("publisher must be at most 40 characters")
	}
	if len([]rune(m.Description)) > 100 {
		bad("description must be at most 100 characters (it has %d)", len([]rune(m.Description)))
	}
	if !semverRe.MatchString(m.Version) {
		bad("version %q is not semver (e.g. 1.0.0)", m.Version)
	}
	if m.APIVersion < 1 || m.APIVersion > APIVersion {
		bad("apiVersion %d not supported (this controller speaks %d)", m.APIVersion, APIVersion)
	}
	seen := map[string]bool{}
	for _, t := range m.ConnectionTypes {
		if !idRe.MatchString(t.ID) || seen[t.ID] {
			bad("connection type id %q is invalid or repeated", t.ID)
		}
		seen[t.ID] = true
		if len(t.Capabilities) == 0 {
			bad("connection type %s has no capabilities", t.ID)
		}
		for _, c := range t.Capabilities {
			if c != CapSwitcher && c != CapSlides && c != CapMixer && c != CapAssistant {
				bad("connection type %s: unknown capability %q", t.ID, c)
			}
		}
	}
	for _, a := range m.Actions {
		if !actionRe.MatchString(a.ID) {
			bad("action id %q: use snake_case", a.ID)
		}
	}
	for _, w := range m.Widgets {
		if w.Sample == nil {
			bad("widget %s has no sample data for its preview", w.Type)
		}
		if w.Kind != "core" && w.Kind != "list" && w.Kind != "status" && w.Kind != "chat" {
			bad("widget %s: kind must be core, list, status or chat", w.Type)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("manifest: %s", strings.Join(errs, "; "))
	}
	return nil
}
