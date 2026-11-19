package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"ptz/internal/cams"
	"strings"

	"ptz/internal/macros"
)

func (s *Server) macroEnv() macros.Env {
	env := macros.Env{
		Favorite: func(name string) (string, int, bool) {
			f, ok := s.Devs().FavoriteByName(name)
			if !ok {
				return "", 0, false
			}
			ch := 0
			if f.Channel != nil {
				ch = *f.Channel
			}
			return f.Label, ch, true
		},
		FavoriteLabels: func() []string {
			var l []string
			for _, f := range s.Devs().Favorites {
				l = append(l, f.Label)
			}
			return l
		},
		PresetExists: func(name string) bool { _, ok := s.presetSlot(name); return ok },
	}
	if s.Devs().Mixer != nil {
		env.MaxChannel = s.Devs().Mixer.MaxChannel()
	}
	return env
}

// mainCam is the camera macros and agents act on.
func (s *Server) mainCam() (*cams.Unit, error) {
	u := s.Cams.Main()
	if u == nil {
		return nil, fmt.Errorf("no camera set up yet")
	}
	return u, nil
}

func (s *Server) presetSlot(name string) (int, bool) {
	u, err := s.mainCam()
	if err != nil {
		return 0, false
	}
	l, err := u.Presets.List()
	if err != nil {
		return 0, false
	}
	for _, p := range l {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			return p.Slot, true
		}
	}
	return 0, false
}

// MacroActions wires the engine to this server's devices and camera.
// Every action looks up the CURRENT device set when it runs, so devices
// changed in Settings take effect for macros immediately.
func (s *Server) MacroActions() macros.Actions {
	noSwitcher := fmt.Errorf("switcher not set up")
	return macros.Actions{
		MuteChannel: func(ctx context.Context, ch int, muted bool) (bool, error) {
			return s.Devs().MuteChannel(ctx, ch, muted)
		},
		SwitcherPreview: func(ctx context.Context, in int) error {
			if sw := s.Devs().Switcher; sw != nil {
				return sw.Preview(ctx, in)
			}
			return noSwitcher
		},
		SwitcherCut: func(ctx context.Context) error {
			if sw := s.Devs().Switcher; sw != nil {
				return sw.Cut(ctx)
			}
			return noSwitcher
		},
		SwitcherFade: func(ctx context.Context, ms int) error {
			if sw := s.Devs().Switcher; sw != nil {
				return sw.Fade(ctx, ms)
			}
			return noSwitcher
		},
		Slides: func(ctx context.Context, action string) error {
			p := s.Devs().Presentation
			if p == nil {
				return fmt.Errorf("slides not set up")
			}
			_, err := p.Do(ctx, action)
			return err
		},
		CameraPreset: func(ctx context.Context, name string) error {
			slot, ok := s.presetSlot(name)
			if !ok {
				return fmt.Errorf("no camera preset named %q", name)
			}
			u, err := s.mainCam()
			if err != nil {
				return err
			}
			if !u.Svc.Connected() {
				return fmt.Errorf("camera not connected")
			}
			if u.Svc.OperationActive() {
				return fmt.Errorf("camera is busy (white-balance pick in progress)")
			}
			return u.Svc.RecallPreset(slot)
		},
	}
}

func (s *Server) macroRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/macros", s.listMacros)
	mux.HandleFunc("GET /api/macros/engine", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.Macros.Status())
	})
	mux.HandleFunc("POST /api/macros/import", s.importMacros)
	mux.HandleFunc("POST /api/macros", s.createMacro)
	mux.HandleFunc("GET /api/macros/{id}", s.getMacro)
	mux.HandleFunc("PATCH /api/macros/{id}", s.patchMacro)
	mux.HandleFunc("DELETE /api/macros/{id}", s.deleteMacro)
	mux.HandleFunc("POST /api/macros/{id}/run", s.runMacro)
}

func macroErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, macros.ErrValidation):
		fail(w, 400, strings.TrimPrefix(err.Error(), macros.ErrValidation.Error()+": "))
	case errors.Is(err, macros.ErrConflict):
		fail(w, 409, strings.TrimPrefix(err.Error(), macros.ErrConflict.Error()+": "))
	case errors.Is(err, macros.ErrNotFound):
		fail(w, 404, strings.TrimPrefix(err.Error(), macros.ErrNotFound.Error()+": "))
	default:
		fail(w, 500, err.Error())
	}
}

func decodeAny(w http.ResponseWriter, r *http.Request) (any, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var v any
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		fail(w, 400, "the body is not valid JSON")
		return nil, false
	}
	return v, true
}

func (s *Server) listMacros(w http.ResponseWriter, r *http.Request) {
	l, err := s.MacroStore.List()
	if err != nil {
		macroErr(w, err)
		return
	}
	writeJSON(w, 200, l)
}

func (s *Server) getMacro(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := s.MacroStore.Get(id)
	if err != nil {
		macroErr(w, err)
		return
	}
	if m == nil {
		fail(w, 404, "macro not found")
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) createMacro(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeAny(w, r)
	if !ok {
		return
	}
	m, err := macros.Validate(raw, s.macroEnv())
	if err != nil {
		macroErr(w, err)
		return
	}
	c, err := s.MacroStore.Create(m)
	if err != nil {
		macroErr(w, err)
		return
	}
	writeJSON(w, 201, c)
}

func (s *Server) patchMacro(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cur, err := s.MacroStore.Get(id)
	if err != nil || cur == nil {
		fail(w, 404, "macro not found")
		return
	}
	raw, ok := decodeAny(w, r)
	if !ok {
		return
	}
	patch, ok := raw.(map[string]any)
	if !ok {
		fail(w, 400, "a macro patch must be a JSON object")
		return
	}
	// Merge onto the current document, then validate the whole thing.
	stepsJSON, _ := json.Marshal(cur.Steps)
	var steps any
	json.Unmarshal(stepsJSON, &steps)
	doc := map[string]any{"name": cur.Name, "description": cur.Description, "steps": steps}
	for k, v := range patch {
		if v != nil {
			doc[k] = v
		}
	}
	m, err := macros.Validate(doc, s.macroEnv())
	if err != nil {
		macroErr(w, err)
		return
	}
	u, err := s.MacroStore.Update(id, m)
	if err != nil {
		macroErr(w, err)
		return
	}
	writeJSON(w, 200, u)
}

func (s *Server) deleteMacro(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	found, err := s.MacroStore.Delete(id)
	if err != nil {
		macroErr(w, err)
		return
	}
	if !found {
		fail(w, 404, "macro not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) runMacro(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// Runs survive the client hanging up: a half-run macro on live
	// audio is worse than a finished one nobody watched.
	rep, err := s.Macros.Run(context.WithoutCancel(r.Context()), id)
	if err != nil {
		macroErr(w, err)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) importMacros(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeAny(w, r)
	if !ok {
		return
	}
	items, dry, err := macros.ExtractImport(raw)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": strings.TrimPrefix(err.Error(), macros.ErrValidation.Error()+": "), "errors": []any{}})
		return
	}
	if r.URL.Query().Get("validateOnly") == "true" {
		dry = true
	}
	var good []*macros.Macro
	var errs []map[string]any
	seen := map[string]int{}
	env := s.macroEnv()
	for i, it := range items {
		var label any
		if mm, ok := it.(map[string]any); ok {
			label = mm["name"]
		}
		m, err := macros.Validate(it, env)
		if err != nil {
			errs = append(errs, map[string]any{"index": i, "name": label,
				"error": strings.TrimPrefix(err.Error(), macros.ErrValidation.Error()+": ")})
			continue
		}
		key := strings.ToLower(m.Name)
		if first, dup := seen[key]; dup {
			errs = append(errs, map[string]any{"index": i, "name": m.Name,
				"error": fmt.Sprintf("duplicate name within the import (first at index %d)", first)})
			continue
		}
		seen[key] = i
		if ex, _ := s.MacroStore.ByName(m.Name); ex != nil {
			errs = append(errs, map[string]any{"index": i, "name": m.Name,
				"error": fmt.Sprintf("a macro named %q already exists", m.Name)})
			continue
		}
		good = append(good, m)
	}
	if len(errs) > 0 {
		writeJSON(w, 400, map[string]any{
			"detail": fmt.Sprintf("%d of %d macro(s) failed validation; nothing was imported", len(errs), len(items)),
			"errors": errs})
		return
	}
	if dry {
		writeJSON(w, 200, map[string]any{"valid": true, "validateOnly": true, "macros": good})
		return
	}
	var created []*macros.Macro
	for _, m := range good {
		c, err := s.MacroStore.Create(m)
		if err != nil {
			macroErr(w, err)
			return
		}
		created = append(created, c)
	}
	writeJSON(w, 201, map[string]any{"imported": created})
}
