package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
	"ptz/internal/devices"
)

// Settings → Connections: any number of connections, each through an
// add-on's connection type. Saving rewrites devices.json (the app owns
// that file) and swaps the live set without a restart.

func (s *Server) connectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/connections", s.listConnections)
	mux.HandleFunc("POST /api/connections", s.createConnection)
	mux.HandleFunc("PUT /api/connections/{id}", s.updateConnection)
	mux.HandleFunc("DELETE /api/connections/{id}", s.deleteConnection)
	mux.HandleFunc("POST /api/connections/test", s.testConnection)
	mux.HandleFunc("GET /api/connections/options/{addon}/{name}", func(w http.ResponseWriter, r *http.Request) {
		o := []addon.Option{}
		if s.Addons != nil {
			if got := s.Addons.Options(r.PathValue("addon"), r.PathValue("name")); got != nil {
				o = got
			}
		}
		writeJSON(w, 200, o)
	})
	mux.HandleFunc("GET /api/connections/{id}/state", s.connectionState)
	mux.HandleFunc("POST /api/connections/{id}/actions/{action}", s.connectionAction)
}

type connBody struct {
	Addon        string             `json:"addon"`
	Type         string             `json:"type"`
	Name         string             `json:"name"`
	Config       map[string]any     `json:"config"`
	Favorites    []devices.Favorite `json:"favorites"`
	CameraInputs map[string]int     `json:"cameraInputs"`
	Disabled     bool               `json:"disabled"`
}

// secretKeys are the password fields of a connection type.
func (s *Server) secretKeys(addonID, typ string) []string {
	if s.Addons == nil {
		return nil
	}
	t, ok := s.Addons.ConnectionType(addonID, typ)
	if !ok {
		return nil
	}
	var out []string
	for _, f := range t.Fields {
		if f.Type == "password" {
			out = append(out, f.Key)
		}
	}
	return out
}

// masked returns a connection's config without secrets (they're write-only).
func (s *Server) masked(c devices.Connection) map[string]any {
	cfg := map[string]any{}
	json.Unmarshal(c.Config, &cfg)
	for _, k := range s.secretKeys(c.Addon, c.Type) {
		if v, _ := cfg[k].(string); v != "" {
			cfg[k] = ""
			cfg["_has_"+k] = true
		}
	}
	return cfg
}

func (s *Server) connView(l *devices.Live) map[string]any {
	caps := l.Caps
	if len(caps) == 0 && s.Addons != nil {
		caps = s.Addons.DeclaredCaps(l.Connection)
	}
	v := map[string]any{"id": l.ID, "addon": l.Addon, "type": l.Type, "name": l.Name, "config": s.masked(l.Connection),
		"favorites": l.Favorites, "cameraInputs": l.CameraInputs, "disabled": l.Disabled, "capabilities": caps, "ready": l.Impl != nil, "error": errText(l.Err)}
	if v["favorites"] == nil {
		v["favorites"] = []devices.Favorite{}
	}
	if s.Addons != nil {
		if m, ok := s.Addons.Manifest(l.Addon); ok {
			v["addonName"] = m.Name
			for _, t := range m.ConnectionTypes {
				if t.ID == l.Type {
					v["typeLabel"] = t.Label
				}
			}
		}
	}
	return v
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, l := range s.Devs().Conns {
		out = append(out, s.connView(l))
	}
	writeJSON(w, 200, map[string]any{"connections": out, "path": s.DevicesPath})
}

func (s *Server) currentFile() *devices.File {
	s.devMu.RLock()
	defer s.devMu.RUnlock()
	f := &devices.File{Version: 2}
	if s.DevFile != nil {
		f.Connections = append(f.Connections, s.DevFile.Connections...)
	}
	return f
}

// toConnection validates a body against the add-on's connection type.
func (s *Server) toConnection(b connBody, prev *devices.Connection) (devices.Connection, error) {
	c := devices.Connection{Addon: b.Addon, Type: b.Type, Name: strings.TrimSpace(b.Name), Favorites: b.Favorites, Disabled: b.Disabled}
	for cam, in := range b.CameraInputs {
		if in < 1 || in > 999 {
			return c, fmt.Errorf("input %d for a camera is outside 1-999", in)
		}
		if c.CameraInputs == nil {
			c.CameraInputs = map[string]int{}
		}
		c.CameraInputs[cam] = in
	}
	if s.Addons == nil {
		return c, errors.New("no add-on host")
	}
	t, ok := s.Addons.ConnectionType(b.Addon, b.Type)
	if !ok {
		return c, fmt.Errorf("the %q add-on has no connection type %q — is it installed and on?", b.Addon, b.Type)
	}
	if c.Name == "" {
		c.Name = t.Label
	}
	if len([]rune(c.Name)) > 60 {
		return c, errors.New("name is too long (60 characters at most)")
	}
	cfg := b.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	for k := range cfg {
		if strings.HasPrefix(k, "_has_") {
			delete(cfg, k)
		}
	}
	// Secrets are write-only: blank means "keep the saved one".
	if prev != nil && prev.Addon == b.Addon && prev.Type == b.Type {
		old := map[string]any{}
		json.Unmarshal(prev.Config, &old)
		for _, k := range s.secretKeys(b.Addon, b.Type) {
			if v, _ := cfg[k].(string); v == "" {
				if ov, ok := old[k]; ok {
					cfg[k] = ov
				}
			}
		}
	}
	for _, f := range t.Fields {
		if f.Required {
			if v, ok := cfg[f.Key]; !ok || v == nil || v == "" {
				return c, fmt.Errorf("%s is required", f.Label)
			}
		}
	}
	for i, fav := range c.Favorites {
		if fav.Channel < 0 || fav.Channel > 128 {
			return c, fmt.Errorf("quick channel %d: channel %d is out of range", i+1, fav.Channel)
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return c, err
	}
	c.Config = raw
	return c, nil
}

// apply saves the file and swaps the live set.
func (s *Server) applyConnections(w http.ResponseWriter, f *devices.File, what string) bool {
	if s.DevicesPath != "" {
		if err := WriteDevices(s.DevicesPath, f); err != nil {
			fail(w, 500, "could not save: "+err.Error())
			return false
		}
	}
	// Close the old set first (a MIDI port can only be open once).
	if old := s.Devs(); old != nil {
		old.Close()
	}
	var open devices.Opener
	if s.Addons != nil {
		open = s.Addons.Open
	}
	set := devices.Open(f, open)
	s.devMu.Lock()
	s.Dev, s.DevFile = set, f
	s.devMu.Unlock()
	s.pruneGrants(f)
	s.Log.Info("connections "+what, "count", len(f.Connections))
	return true
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var b connBody
	if !decode(w, r, &b) {
		return
	}
	c, err := s.toConnection(b, nil)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	f := s.currentFile()
	c.ID = devices.NewID(f, slug(c.Name, c.Addon))
	f.Connections = append(f.Connections, c)
	if !s.applyConnections(w, f, "added") {
		return
	}
	s.Log.Info("connection added", "connection", c.Name, "addon", c.Addon, "type", c.Type)
	if l := s.Devs().Get(c.ID); l != nil {
		writeJSON(w, 201, s.connView(l))
		return
	}
	writeJSON(w, 201, map[string]any{"id": c.ID})
}

func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var b connBody
	if !decode(w, r, &b) {
		return
	}
	f := s.currentFile()
	idx := -1
	for i, c := range f.Connections {
		if c.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		fail(w, 404, "no such connection")
		return
	}
	prev := f.Connections[idx]
	if b.Addon == "" {
		b.Addon, b.Type = prev.Addon, prev.Type
	}
	c, err := s.toConnection(b, &prev)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	c.ID = id
	f.Connections[idx] = c
	if !s.applyConnections(w, f, "changed") {
		return
	}
	s.Log.Info("connection changed", "connection", c.Name, "addon", c.Addon)
	writeJSON(w, 200, s.connView(s.Devs().Get(id)))
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f := s.currentFile()
	var keep []devices.Connection
	var gone *devices.Connection
	for _, c := range f.Connections {
		if c.ID == id {
			c := c
			gone = &c
			continue
		}
		keep = append(keep, c)
	}
	if gone == nil {
		fail(w, 404, "no such connection")
		return
	}
	f.Connections = keep
	if !s.applyConnections(w, f, "removed") {
		return
	}
	s.Log.Info("connection removed", "connection", gone.Name, "addon", gone.Addon)
	w.WriteHeader(204)
}

// testConnection opens the form's settings on a throwaway connection
// (or the live one when nothing changed, so a MIDI port isn't opened
// twice) and checks that the gear answers.
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	var b struct {
		connBody
		ID string `json:"id"`
	}
	if !decode(w, r, &b) {
		return
	}
	var prev *devices.Connection
	if b.ID != "" {
		if l := s.Devs().Get(b.ID); l != nil {
			prev = &l.Connection
		}
	}
	c, err := s.toConnection(b.connBody, prev)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var impl any
	if prev != nil && string(prev.Config) == string(c.Config) && prev.Type == c.Type {
		if l := s.Devs().Get(b.ID); l != nil && l.Impl != nil {
			impl = l.Impl
		}
	}
	if impl == nil {
		c.ID = fmt.Sprintf("test-%d", time.Now().UnixNano())
		var err error
		impl, _, err = s.Addons.Open(c)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if cl, ok := impl.(devices.Closer); ok {
			defer cl.Close()
		}
		defer s.revokeGrant(c.ID)
	}
	t, _ := s.Addons.ConnectionType(c.Addon, c.Type)
	res := map[string]any{"ok": false}
	for _, capName := range t.Capabilities {
		switch capName {
		case addon.CapSwitcher:
			st, err := impl.(devices.Switcher).State(ctx)
			if err != nil {
				res["error"] = err.Error()
				break
			}
			res["ok"], res["detail"] = true, fmt.Sprintf("Connected — %d inputs, program %d, preview %d", len(st.Inputs), st.Active, st.Preview)
		case addon.CapSlides:
			info := impl.(devices.Presentation).Info()
			if !info.Available {
				res["error"] = info.Target
				break
			}
			res["ok"], res["detail"] = true, "Ready — "+info.Target
			if st, ok := impl.(addon.Stater); ok {
				if m, err := st.AddonState(ctx); err == nil && m["onAir"] == true {
					res["detail"] = fmt.Sprintf("Connected — on air, %v", m["title"])
				}
			}
		case addon.CapAssistant:
			out, err := addon.Dispatch(ctx, impl, "addon.action", mustJSON(map[string]any{"name": "test"}))
			if err != nil {
				res["error"] = err.Error()
				break
			}
			res["ok"], res["detail"] = true, "Connected"
			if m, ok := out.(map[string]any); ok && m["detail"] != nil {
				res["detail"] = m["detail"]
			}
		case addon.CapMixer:
			id, err := impl.(devices.Mixer).Probe(ctx)
			if err != nil {
				res["error"] = err.Error()
				break
			}
			res["ok"], res["detail"] = true, "Console answered — "+id
		}
		if res["ok"] != true {
			break
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && res["ok"] == false && res["error"] == nil {
		res["error"] = "no answer within 6 s"
	}
	writeJSON(w, 200, res)
}

func (s *Server) connectionState(w http.ResponseWriter, r *http.Request) {
	l := s.Devs().Get(r.PathValue("id"))
	if l == nil {
		fail(w, 404, "no such connection")
		return
	}
	if l.Impl == nil {
		writeJSON(w, 200, map[string]any{"error": errText(l.Err)})
		return
	}
	ctx, cancel := short(r)
	defer cancel()
	st, err := addon.Dispatch(ctx, l.Impl, "addon.state", nil)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) connectionAction(w http.ResponseWriter, r *http.Request) {
	l := s.Devs().Get(r.PathValue("id"))
	if l == nil {
		fail(w, 404, "no such connection")
		return
	}
	if l.Impl == nil {
		fail(w, 503, l.Name+" is not connected: "+errText(l.Err))
		return
	}
	var params json.RawMessage
	if r.ContentLength != 0 {
		if !decode(w, r, &params) {
			return
		}
	}
	ctx, cancel := short(r)
	defer cancel()
	action := r.PathValue("action")
	out, err := addon.Dispatch(ctx, l.Impl, "addon.action", mustJSON(map[string]any{"name": action, "params": params}))
	switch {
	case errors.Is(err, addon.ErrUnknownAction):
		fail(w, 400, err.Error())
	case err != nil:
		fail(w, 502, err.Error())
	default:
		s.Log.Info("add-on action", "connection", l.Name, "action", action, "client", clientIP(r))
		writeJSON(w, 200, map[string]any{"ok": true, "result": out})
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func slug(name, fallback string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 32 {
		out = strings.Trim(out[:32], "-")
	}
	if out == "" {
		return fallback
	}
	return out
}

// WriteDevices saves devices.json atomically.
func WriteDevices(path string, f *devices.File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f.Version = 2
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
