package addon

import (
	"fmt"
	"path"
	"regexp"
	"slices"
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
//
// Kinds: core, status, list, chat (apiVersion 1) and, from apiVersion 2,
// the shared shapes image, buttons, input, strip, text, clock, sections,
// slider plus frame (the add-on draws it: Entry is an HTML file under
// ui/ in the package). Bind says, per kind, which state keys and which
// action (Manifest.Actions) the widget uses; see design/WIDGETS-V2.md.
type WidgetDef struct {
	Kind        string         `json:"kind"` // see WidgetKinds
	Type        string         `json:"type"` // core: the built-in widget type; else the add-on's widget id
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	Bind        map[string]any `json:"bind,omitempty"`     // declarative: which state keys / action to use
	Settings    map[string]any `json:"settings,omitempty"` // core: widget settings for the preview
	Size        *Size          `json:"size,omitempty"`     // v2: default grid size (desktop); the host clamps it
	MinPx       *Size          `json:"minPx,omitempty"`    // v2: smallest box it still works in, in px
	Entry       string         `json:"entry,omitempty"`    // frame: the page it loads, e.g. "ui/live.html"
	// Fields are per-widget settings (v2.1), shown in the widget's
	// settings panel and saved with the widget on the dashboard. A bind
	// value "$settings.<key>" reads one; frames get them all in
	// init.settings; a SettingsStater gets them with addon.state. Keys
	// must be unique and can't be connection, addon or widget (the
	// controller's own widget settings).
	Fields []Field        `json:"fields,omitempty"`
	Sample map[string]any `json:"sample"`
}

// SettingsRef is the prefix of a bind value that names a widget setting
// (WidgetDef.Fields) instead of a state key: "$settings.channels".
const SettingsRef = "$settings."

// ReservedWidgetSettings are widget settings the controller itself keeps.
var ReservedWidgetSettings = []string{"connection", "addon", "widget"}

// Size is a width and height: grid cells for WidgetDef.Size, pixels
// for WidgetDef.MinPx.
type Size struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Widget kinds.
const (
	KindCore     = "core"
	KindStatus   = "status"
	KindList     = "list"
	KindChat     = "chat"
	KindImage    = "image"    // v2
	KindButtons  = "buttons"  // v2
	KindInput    = "input"    // v2
	KindStrip    = "strip"    // v2
	KindText     = "text"     // v2
	KindClock    = "clock"    // v2
	KindSections = "sections" // v2
	KindSlider   = "slider"   // v2
	KindFrame    = "frame"    // v2: drawn by the add-on in a sandboxed frame
)

var widgetKinds = map[string]int{ // kind → first apiVersion that has it
	KindCore: 1, KindStatus: 1, KindList: 1, KindChat: 1,
	KindImage: 2, KindButtons: 2, KindInput: 2, KindStrip: 2, KindText: 2,
	KindClock: 2, KindSections: 2, KindSlider: 2, KindFrame: 2,
}

// WidgetKinds lists the widget kinds a manifest of apiVersion may use.
func WidgetKinds(apiVersion int) []string {
	var out []string
	for _, k := range []string{KindCore, KindStatus, KindList, KindChat, KindImage, KindButtons, KindInput,
		KindStrip, KindText, KindClock, KindSections, KindSlider, KindFrame} {
		if widgetKinds[k] <= apiVersion {
			out = append(out, k)
		}
	}
	return out
}

// KnownWidgetKind reports whether this SDK knows kind for a manifest of
// apiVersion.
func KnownWidgetKind(kind string, apiVersion int) bool {
	v, ok := widgetKinds[kind]
	return ok && v <= apiVersion
}

var capabilities = map[string]int{ // capability → first apiVersion that has it
	CapSwitcher: 1, CapSlides: 1, CapMixer: 1, CapAssistant: 1, CapTool: 2,
}

// Capabilities lists the connection capabilities a manifest of
// apiVersion may use.
func Capabilities(apiVersion int) []string {
	var out []string
	for _, c := range []string{CapSwitcher, CapSlides, CapMixer, CapAssistant, CapTool} {
		if capabilities[c] <= apiVersion {
			out = append(out, c)
		}
	}
	return out
}

// LowestAPIVersion returns the manifest with apiVersion lowered to the
// oldest protocol its widgets and connection capabilities need, when it
// asks for the SDK's own version (addon.APIVersion). An add-on that only
// uses v1 widgets and capabilities keeps working on older controllers
// after a rebuild against a newer SDK; one with a "tool" connection type
// stays at 2. An add-on that pins an explicit older or newer version is
// left as is.
func (m Manifest) LowestAPIVersion() Manifest {
	if m.APIVersion != APIVersion {
		return m
	}
	need := 1
	for _, w := range m.Widgets {
		if v, ok := widgetKinds[w.Kind]; ok && v > need {
			need = v
		}
	}
	for _, t := range m.ConnectionTypes {
		for _, c := range t.Capabilities {
			if v, ok := capabilities[c]; ok && v > need {
				need = v
			}
		}
	}
	m.APIVersion = need
	return m
}

// DropUnknownWidgets removes the widgets whose kind this SDK doesn't know
// (for the manifest's apiVersion) and returns them. A controller calls it
// before Validate, so an add-on made for a newer controller still loads,
// just without those widgets.
func (m *Manifest) DropUnknownWidgets() (dropped []WidgetDef) {
	keep := m.Widgets[:0:0]
	for _, w := range m.Widgets {
		if KnownWidgetKind(w.Kind, m.APIVersion) {
			keep = append(keep, w)
		} else {
			dropped = append(dropped, w)
		}
	}
	if dropped != nil {
		m.Widgets = keep
	}
	return dropped
}

// UI files: what a package's ui/ folder may hold, and how much.
var UIExtensions = []string{".html", ".js", ".css", ".svg", ".png", ".woff2", ".json"}

// MaxUISize is the most a package's ui/ folder may hold in all.
const MaxUISize = 2 << 20

// UIFileAllowed reports whether name (a slash path inside the package)
// is a ui/ file of an allowed type.
func UIFileAllowed(name string) bool {
	if !strings.HasPrefix(name, "ui/") || path.Clean(name) != name || strings.Contains(name, "\\") {
		return false
	}
	ext := strings.ToLower(path.Ext(name))
	for _, e := range UIExtensions {
		if ext == e {
			return true
		}
	}
	return false
}

// required state-key binds per v2 kind (a value that is not "?" in the
// spec's table).
var requiredBind = map[string][]string{
	KindImage:    {"src"},
	KindInput:    {"action", "param"},
	KindStrip:    {"items", "action"},
	KindSections: {"sections", "action"},
	KindText:     {"body"},
	KindSlider:   {"value", "action"},
}

// checkWidget checks one widget of a v2 manifest. actions is the set of
// declared action ids.
func checkWidget(w WidgetDef, actions map[string]bool, bad func(string, ...any)) {
	name := w.Type
	for _, k := range requiredBind[w.Kind] {
		if v, ok := w.Bind[k]; !ok || v == nil || v == "" {
			bad("widget %s: bind.%s is required for a %s widget", name, k, w.Kind)
		}
	}
	checkAction := func(where string, v any) {
		if v == nil {
			return
		}
		s, ok := v.(string)
		if !ok || s == "" {
			bad("widget %s: %s must be an action id", name, where)
			return
		}
		if !actions[s] {
			bad("widget %s: %s %q is not one of the add-on's actions", name, where, s)
		}
	}
	checkAction("bind.action", w.Bind["action"])
	if w.Kind == KindButtons {
		_, hasItems := w.Bind["items"]
		static, hasStatic := w.Bind["buttons"]
		if !hasItems && !hasStatic {
			bad("widget %s: a buttons widget needs bind.items (a state key) or bind.buttons", name)
		}
		if hasStatic {
			list, ok := static.([]any)
			if !ok {
				if typed, ok2 := static.([]map[string]any); ok2 {
					for _, x := range typed {
						list = append(list, x)
					}
					ok = true
				}
			}
			if !ok {
				bad("widget %s: bind.buttons must be a list of buttons", name)
			}
			for i, x := range list {
				b, _ := x.(map[string]any)
				if b == nil {
					bad("widget %s: bind.buttons[%d] is not a button", name, i)
					continue
				}
				if b["action"] != nil {
					checkAction(fmt.Sprintf("bind.buttons[%d].action", i), b["action"])
				} else if w.Bind["action"] == nil {
					bad("widget %s: bind.buttons[%d] has no action and the widget has no bind.action", name, i)
				}
			}
		}
	}
	if w.Kind == KindFrame {
		e := w.Entry
		if e == "" {
			bad("widget %s: a frame widget needs entry (e.g. ui/index.html)", name)
		} else if !UIFileAllowed(e) || !strings.HasSuffix(strings.ToLower(e), ".html") {
			bad("widget %s: entry %q must be an .html file under ui/", name, e)
		}
	} else if w.Entry != "" {
		bad("widget %s: entry is only for frame widgets", name)
	}
	if w.Size != nil && (w.Size.W < 1 || w.Size.H < 1) {
		bad("widget %s: size must be at least 1x1", name)
	}
	if w.MinPx != nil && (w.MinPx.W < 0 || w.MinPx.H < 0) {
		bad("widget %s: minPx can't be negative", name)
	}
}

// checkWidgetFields checks a widget's Fields and that every
// "$settings.<key>" in its bind names one of them.
func checkWidgetFields(w WidgetDef, bad func(string, ...any)) {
	name := w.Type
	keys := map[string]bool{}
	for i, f := range w.Fields {
		switch {
		case f.Key == "":
			bad("widget %s: fields[%d] has no key", name, i)
			continue
		case keys[f.Key]:
			bad("widget %s: field key %q is repeated", name, f.Key)
		case slices.Contains(ReservedWidgetSettings, f.Key):
			bad("widget %s: field key %q is kept by the controller", name, f.Key)
		}
		keys[f.Key] = true
		if !KnownFieldType(f.Type) {
			bad("widget %s: field %s: type must be one of %s", name, f.Key, strings.Join(FieldTypes, ", "))
		}
		if f.Multiple && f.Type != FieldSelect {
			bad("widget %s: field %s: multiple is only for select fields", name, f.Key)
		}
		if (f.Type == FieldSelect || f.Type == FieldOrder) && len(f.Options) == 0 && f.OptionsFrom == "" {
			bad("widget %s: field %s: a %s field needs options or optionsFrom", name, f.Key, f.Type)
		}
	}
	var walk func(where string, v any)
	walk = func(where string, v any) {
		switch t := v.(type) {
		case string:
			if k, ok := strings.CutPrefix(t, SettingsRef); ok && !keys[k] {
				bad("widget %s: %s %q names no field of this widget", name, where, t)
			}
		case map[string]any:
			for k, e := range t {
				walk(where+"."+k, e)
			}
		case []any:
			for i, e := range t {
				walk(fmt.Sprintf("%s[%d]", where, i), e)
			}
		case []map[string]any:
			for i, e := range t {
				walk(fmt.Sprintf("%s[%d]", where, i), e)
			}
		}
	}
	for k, v := range w.Bind {
		walk("bind."+k, v)
	}
}

// Field types.
const (
	FieldText     = "text"
	FieldNumber   = "number"
	FieldSelect   = "select" // Multiple: the value is a list of option values
	FieldBool     = "bool"
	FieldPassword = "password"
	FieldNotes    = "notes"
	FieldJSON     = "json"
	FieldScopes   = "scopes"
	FieldTextarea = "textarea"
	FieldOrder    = "order" // v2.1: an ordered pick from the options (a list of values)
)

// FieldTypes lists every field type.
var FieldTypes = []string{FieldText, FieldNumber, FieldSelect, FieldBool, FieldPassword, FieldNotes, FieldJSON, FieldScopes, FieldTextarea, FieldOrder}

// KnownFieldType reports whether t is a field type.
func KnownFieldType(t string) bool { return slices.Contains(FieldTypes, t) }

// Option is one choice of a select field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field is one setting. Types: text, number, select, bool, password,
// notes (action → MIDI note), json (free-form object), scopes (which
// controller permissions a Controller add-on's connection gets),
// textarea (longer text) and (v2.1) order (pick several of the options
// and put them in order; the value is a list of option values). A select
// with Multiple set also takes a list.
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
	// Multiple (select only, v2.1): pick several; the value is a list.
	Multiple bool `json:"multiple,omitempty"`
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
			v, ok := capabilities[c]
			switch {
			case !ok:
				bad("connection type %s: unknown capability %q (one of %s)", t.ID, c, strings.Join(Capabilities(APIVersion), ", "))
			case v > m.APIVersion:
				bad("connection type %s: capability %s needs apiVersion %d", t.ID, c, v)
			}
		}
	}
	for _, a := range m.Actions {
		if !actionRe.MatchString(a.ID) {
			bad("action id %q: use snake_case", a.ID)
		}
	}
	actions := map[string]bool{}
	for _, a := range m.Actions {
		actions[a.ID] = true
	}
	for _, w := range m.Widgets {
		if w.Sample == nil {
			bad("widget %s has no sample data for its preview", w.Type)
		}
		if !KnownWidgetKind(w.Kind, m.APIVersion) {
			if KnownWidgetKind(w.Kind, APIVersion) {
				bad("widget %s: kind %s needs apiVersion %d", w.Type, w.Kind, widgetKinds[w.Kind])
			} else {
				bad("widget %s: kind must be one of %s", w.Type, strings.Join(WidgetKinds(max(m.APIVersion, 1)), ", "))
			}
			continue
		}
		if m.APIVersion >= 2 {
			checkWidget(w, actions, bad)
		}
		checkWidgetFields(w, bad)
	}
	if len(errs) > 0 {
		return fmt.Errorf("manifest: %s", strings.Join(errs, "; "))
	}
	return nil
}
