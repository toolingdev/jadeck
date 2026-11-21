// Package addons is the controller's add-on host: it knows which add-ons
// are installed (from Settings → Add-ons, from the packages a release
// ships with, or loaded from a folder in dev mode), runs each one as its
// own process, and opens connections through them.
//
// The controller itself contains no product code. Each add-on (vMix,
// Behringer WING, Proclaim, ...) is a separate project built with the
// SDK in sdk/addon, shipped as a signed .ptzaddon file.
package addons

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
	"github.com/toolingdev/jadeck/sdk/addon/pack"
	"ptz/internal/devices"
)

// Info is what Settings → Add-ons shows about one add-on.
type Info struct {
	Manifest    addon.Manifest `json:"manifest"`
	Source      string         `json:"source"` // installed | dev
	Verified    bool           `json:"verified"`
	Enabled     bool           `json:"enabled"`
	Running     bool           `json:"running"`
	Pid         int            `json:"pid,omitempty"`
	Since       *time.Time     `json:"since,omitempty"`
	LastError   string         `json:"lastError,omitempty"`
	CanRollBack bool           `json:"canRollBack,omitempty"`
	Path        string         `json:"path,omitempty"`
}

type devAddon struct {
	Path    string   `json:"path"`
	Command []string `json:"command"`
}

type state struct {
	Disabled []string   `json:"disabled"`
	Dev      []devAddon `json:"dev"`
	Verified []string   `json:"verified"` // installed add-ons whose signature checked out
}

// Host runs add-ons.
type Host struct {
	log         *slog.Logger
	dir         string                // DataDir/addons
	inProcess   map[string]addon.Spec // tests: add-ons run inside this process
	inProcessUI map[string]fs.FS      // tests: their ui/ files

	mu    sync.Mutex
	procs map[string]*proc
	st    state

	// Grant gives a Controller add-on's connection access to the
	// controller (set by the API server); scopes come from the
	// connection's "permissions" field.
	Grant func(c devices.Connection, scopes []string) addon.ControllerAccess

	// PubKey is the release key: packages it signed are "verified".
	PubKey ed25519.PublicKey

	// OnChanged is called when an add-on says a connection's state changed.
	OnChanged func(conn string)

	warned sync.Map // "id@version/type" → widgets of unknown kinds already logged
}

// New makes a host. dir holds installed add-ons and the host's state.
func New(dir string, log *slog.Logger) *Host {
	h := &Host{log: log, dir: dir, procs: map[string]*proc{}}
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		json.Unmarshal(b, &h.st)
	}
	devices.DeclaredCaps = h.DeclaredCaps
	return h
}

// NewInProcess runs the given add-ons inside this process (tests).
func NewInProcess(log *slog.Logger, specs ...addon.Spec) *Host {
	h := New("", log)
	h.inProcess = map[string]addon.Spec{}
	for _, s := range specs {
		h.inProcess[s.Manifest.ID] = s
	}
	return h
}

func (h *Host) saveState() error {
	if h.dir == "" {
		return nil
	}
	if err := os.MkdirAll(h.dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(h.st, "", "  ")
	tmp := filepath.Join(h.dir, "state.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(h.dir, "state.json"))
}

// ------------------------------------------------------------ discovery

type resolved struct {
	m      addon.Manifest
	source string
	argv   []string
	dir    string
}

func exeName() string { return pack.ExeName() }

// installed reads DataDir/addons/<id>/addon.json.
func (h *Host) installed(id string) (addon.Manifest, string, bool) {
	if h.dir == "" {
		return addon.Manifest{}, "", false
	}
	d := filepath.Join(h.dir, id)
	b, err := os.ReadFile(filepath.Join(d, "addon.json"))
	if err != nil {
		return addon.Manifest{}, "", false
	}
	m, err := h.parseManifest(b)
	if err != nil || m.ID != id {
		return addon.Manifest{}, "", false
	}
	if _, err := os.Stat(filepath.Join(d, exeName())); err != nil {
		return addon.Manifest{}, "", false
	}
	return m, d, true
}

func (h *Host) resolve(id string) (resolved, bool) {
	if spec, ok := h.inProcess[id]; ok {
		return resolved{m: spec.Manifest, source: "installed"}, true
	}
	h.mu.Lock()
	devs := append([]devAddon(nil), h.st.Dev...)
	h.mu.Unlock()
	for _, d := range devs {
		if m, err := h.readManifest(d.Path); err == nil && m.ID == id {
			return resolved{m: m, source: "dev", argv: d.Command, dir: d.Path}, true
		}
	}
	if im, idir, ok := h.installed(id); ok {
		return resolved{m: im, source: "installed", argv: []string{filepath.Join(idir, exeName())}, dir: idir}, true
	}
	return resolved{}, false
}

func (h *Host) readManifest(dir string) (addon.Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "addon.json"))
	if err != nil {
		return addon.Manifest{}, err
	}
	return h.parseManifest(b)
}

// parseManifest reads addon.json. Widgets of kinds this controller
// doesn't know (an add-on made for a newer one) are left out with a
// warning instead of refusing the add-on; apiVersion 1 and 2 are accepted.
func (h *Host) parseManifest(b []byte) (addon.Manifest, error) {
	var m addon.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	h.warnDropped(m.ID, m.Version, m.DropUnknownWidgets())
	return m, m.Validate()
}

func (h *Host) warnDropped(id, version string, dropped []addon.WidgetDef) {
	for _, w := range dropped {
		if _, seen := h.warned.LoadOrStore(id+"@"+version+"/"+w.Type, true); seen || h.log == nil {
			continue
		}
		h.log.Warn("add-on widget left out: this controller doesn't know its kind", "addon", id, "version", version, "widget", w.Type, "kind", w.Kind)
	}
}

// ids lists every known add-on id.
func (h *Host) ids() []string {
	set := map[string]bool{}
	for id := range h.inProcess {
		set[id] = true
	}
	if h.dir != "" {
		if ents, err := os.ReadDir(h.dir); err == nil {
			for _, e := range ents {
				if e.IsDir() {
					if _, _, ok := h.installed(e.Name()); ok {
						set[e.Name()] = true
					}
				}
			}
		}
	}
	h.mu.Lock()
	for _, d := range h.st.Dev {
		if m, err := h.readManifest(d.Path); err == nil {
			set[m.ID] = true
		}
	}
	h.mu.Unlock()
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// List describes every add-on: product add-ons first, generic ones last.
func (h *Host) List() []Info {
	var out []Info
	for _, id := range h.ids() {
		r, ok := h.resolve(id)
		if !ok {
			continue
		}
		in := Info{Manifest: r.m, Source: r.source, Enabled: h.Enabled(id), Path: r.dir, CanRollBack: h.HasPrevious(id)}
		in.Verified = r.source == "installed" && (h.isVerified(id) || h.inProcess != nil)
		h.mu.Lock()
		p := h.procs[id]
		h.mu.Unlock()
		if p != nil {
			running, pid, since, lastErr := p.status()
			in.Running, in.Pid, in.LastError = running, pid, lastErr
			if running {
				in.Since = &since
			}
		}
		out = append(out, in)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Manifest.Generic != out[j].Manifest.Generic {
			return !out[i].Manifest.Generic
		}
		return strings.ToLower(out[i].Manifest.Name) < strings.ToLower(out[j].Manifest.Name)
	})
	return out
}

// Manifest returns an add-on's manifest.
func (h *Host) Manifest(id string) (addon.Manifest, bool) {
	r, ok := h.resolve(id)
	return r.m, ok
}

// ConnectionType finds an add-on's connection type.
func (h *Host) ConnectionType(addonID, typ string) (addon.ConnectionType, bool) {
	m, ok := h.Manifest(addonID)
	if !ok {
		return addon.ConnectionType{}, false
	}
	for _, t := range m.ConnectionTypes {
		if t.ID == typ {
			return t, true
		}
	}
	return addon.ConnectionType{}, false
}

// DeclaredCaps is what a connection's type says it can do, from the
// manifest (used while it can't be opened).
func (h *Host) DeclaredCaps(c devices.Connection) []string {
	t, ok := h.ConnectionType(c.Addon, c.Type)
	if !ok {
		return nil
	}
	return t.Capabilities
}

func (h *Host) isVerified(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, v := range h.st.Verified {
		if v == id {
			return true
		}
	}
	return false
}

// Enabled reports whether an add-on is turned on.
func (h *Host) Enabled(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range h.st.Disabled {
		if d == id {
			return false
		}
	}
	return true
}

// ------------------------------------------------------------- running

// proc returns the running process for an add-on, starting it.
func (h *Host) proc(id string) (*proc, error) {
	if !h.Enabled(id) {
		return nil, fmt.Errorf("the %s add-on is turned off (Settings → Add-ons)", id)
	}
	h.mu.Lock()
	p := h.procs[id]
	h.mu.Unlock()
	if p != nil {
		if running, _, _, _ := p.status(); running {
			return p, nil
		}
	}
	r, ok := h.resolve(id)
	if !ok {
		return nil, fmt.Errorf("add-on %q is not installed", id)
	}
	if len(r.argv) == 0 || r.argv[0] == "" {
		return nil, fmt.Errorf("add-on %q has no program to run", id)
	}
	if p == nil {
		p = &proc{id: id, log: h.log, opens: map[string]addon.OpenParams{}}
		p.onChanged = func(conn string) {
			if h.OnChanged != nil {
				h.OnChanged(conn)
			}
		}
	}
	p.argv, p.dir = r.argv, r.dir
	p.stopping = false
	h.mu.Lock()
	h.procs[id] = p
	h.mu.Unlock()
	if err := p.boot(); err != nil {
		return nil, err
	}
	return p, nil
}

// Open opens a connection through its add-on (a devices.Opener).
func (h *Host) Open(c devices.Connection) (any, []string, error) {
	cfg := c.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	if h.inProcess != nil {
		spec, ok := h.inProcess[c.Addon]
		if !ok {
			return nil, nil, fmt.Errorf("add-on %q is not installed", c.Addon)
		}
		open := spec.Open[c.Type]
		if open == nil {
			return nil, nil, fmt.Errorf("the %s add-on has no connection type %q", spec.Manifest.Name, c.Type)
		}
		impl, err := open(cfg)
		if err != nil {
			return nil, nil, err
		}
		return impl, addon.Caps(impl), nil
	}
	if _, ok := h.ConnectionType(c.Addon, c.Type); !ok {
		if _, has := h.Manifest(c.Addon); !has {
			return nil, nil, fmt.Errorf("add-on %q is not installed", c.Addon)
		}
		return nil, nil, fmt.Errorf("the %s add-on has no connection type %q", c.Addon, c.Type)
	}
	if m, _ := h.Manifest(c.Addon); m.Controller && h.Grant != nil {
		var cm map[string]any
		json.Unmarshal(cfg, &cm)
		if cm == nil {
			cm = map[string]any{}
		}
		var scopes []string
		if l, ok := cm["permissions"].([]any); ok {
			for _, x := range l {
				if s, ok := x.(string); ok {
					scopes = append(scopes, s)
				}
			}
		}
		cm["_controller"] = h.Grant(c, scopes)
		cfg, _ = json.Marshal(cm)
	}
	p, err := h.proc(c.Addon)
	if err != nil {
		return nil, nil, err
	}
	params := addon.OpenParams{Conn: c.ID, Type: c.Type, Config: cfg}
	ctx, cancel := quick()
	defer cancel()
	var res addon.OpenResult
	if err := p.call(ctx, "open", params, &res); err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	p.opens[c.ID] = params
	p.mu.Unlock()
	return &remote{p: p, conn: c.ID}, res.Capabilities, nil
}

// Options asks an add-on for a live option list (e.g. MIDI ports).
func (h *Host) Options(addonID, name string) []addon.Option {
	if h.inProcess != nil {
		if spec, ok := h.inProcess[addonID]; ok && spec.Options[name] != nil {
			return spec.Options[name]()
		}
		return nil
	}
	p, err := h.proc(addonID)
	if err != nil {
		return nil
	}
	ctx, cancel := quick()
	defer cancel()
	var out []addon.Option
	p.call(ctx, "options", map[string]string{"name": name}, &out)
	return out
}

// SetEnabled turns an add-on on or off. Off stops its process; its
// connections stay saved and show that the add-on is off.
func (h *Host) SetEnabled(id string, on bool) error {
	h.mu.Lock()
	var d []string
	for _, x := range h.st.Disabled {
		if x != id {
			d = append(d, x)
		}
	}
	if !on {
		d = append(d, id)
	}
	h.st.Disabled = d
	p := h.procs[id]
	err := h.saveState()
	h.mu.Unlock()
	if !on && p != nil {
		p.stop()
	}
	return err
}

// Restart stops an add-on's process; the next Open starts the current
// version (after an update or a dev change).
func (h *Host) Restart(id string) {
	h.mu.Lock()
	p := h.procs[id]
	delete(h.procs, id)
	h.mu.Unlock()
	if p != nil {
		p.stop()
	}
}

// Close stops every add-on.
func (h *Host) Close() {
	h.mu.Lock()
	ps := make([]*proc, 0, len(h.procs))
	for _, p := range h.procs {
		ps = append(ps, p)
	}
	h.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		go func() { defer wg.Done(); p.stop() }()
	}
	wg.Wait()
}

// ---------------------------------------------------------------- dev

// LoadDev adds an add-on folder in development mode: addon.json plus a
// command that runs it (e.g. ["go", "run", "."]).
func (h *Host) LoadDev(path string, command []string) (addon.Manifest, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return addon.Manifest{}, err
	}
	m, err := h.readManifest(path)
	if err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	if len(command) == 0 {
		command = []string{"go", "run", "."}
	}
	h.mu.Lock()
	var keep []devAddon
	for _, d := range h.st.Dev {
		if dm, err := h.readManifest(d.Path); d.Path != path && (err != nil || dm.ID != m.ID) {
			keep = append(keep, d)
		}
	}
	h.st.Dev = append(keep, devAddon{Path: path, Command: command})
	err = h.saveState()
	h.mu.Unlock()
	h.Restart(m.ID)
	return m, err
}

// UnloadDev removes a dev add-on.
func (h *Host) UnloadDev(id string) error {
	h.mu.Lock()
	var keep []devAddon
	for _, d := range h.st.Dev {
		if m, err := h.readManifest(d.Path); err != nil || m.ID != id {
			keep = append(keep, d)
		}
	}
	h.st.Dev = keep
	err := h.saveState()
	h.mu.Unlock()
	h.Restart(id)
	return err
}

// ------------------------------------------------------------ versions

// newer reports a > b for semver strings (pre-release ignored).
func newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, s := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(s)
	}
	return out
}

// Newer is exported for the update checker.
func Newer(a, b string) bool { return newer(a, b) }

var ErrNotFound = errors.New("not found")
