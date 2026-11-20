package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ptz/internal/auth"
	"ptz/internal/macros"
	"ptz/internal/schedule"
)

// Scheduled actions ("Coming up"): run macro steps or a saved macro at a
// set time. Screens see them in the Coming up widget, where anyone can
// cancel or run one now.

func (s *Server) scheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/schedule", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"jobs": s.Schedule.List(), "now": time.Now()})
	})
	mux.HandleFunc("POST /api/schedule", s.addSchedule)
	mux.HandleFunc("DELETE /api/schedule/{id}", func(w http.ResponseWriter, r *http.Request) {
		if s.scheduleErr(w, s.Schedule.Cancel(r.PathValue("id"), whoAsked(r, ""))) {
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/schedule/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		if s.scheduleErr(w, s.Schedule.RunNow(r.PathValue("id"))) {
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
}

func (s *Server) scheduleErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, schedule.ErrNotFound):
		fail(w, 404, err.Error())
	default:
		fail(w, 422, err.Error())
	}
	return true
}

// stepScope is the key permission a step needs.
func stepScope(st macros.Step) string {
	switch st.Type {
	case "switcher":
		return "switcher"
	case "slides":
		return "slides"
	case "mixer_mute":
		return "mixer"
	case "camera_preset":
		return "camera.move"
	case "addon":
		return "addons"
	}
	return ""
}

// keyLabel names who used a key: an add-on connection (the AI) by its
// own name, anything else as "key <name>".
func keyLabel(k *auth.Key) string {
	if n, ok := strings.CutSuffix(k.Name, " (add-on)"); ok {
		return n
	}
	return "key " + k.Name
}

func whoAsked(r *http.Request, by string) string {
	if k, ok := r.Context().Value(keyCtx).(*auth.Key); ok && k != nil {
		return keyLabel(k)
	}
	if by != "" {
		return by
	}
	return "someone at " + clientIP(r)
}

type scheduleBody struct {
	At        *time.Time `json:"at,omitempty"`
	InSeconds int        `json:"inSeconds,omitempty"`
	Label     string     `json:"label,omitempty"`
	Macro     string     `json:"macro,omitempty"`
	Steps     []any      `json:"steps,omitempty"`
	By        string     `json:"by,omitempty"` // a screen or the Assistant naming itself
}

// buildJob validates a schedule request; key (may be nil) must hold the
// permission for every step.
func (s *Server) buildJob(b scheduleBody, key *auth.Key) (schedule.Job, error) {
	j := schedule.Job{Label: strings.TrimSpace(b.Label)}
	switch {
	case b.At != nil:
		j.At = *b.At
	case b.InSeconds > 0:
		j.At = time.Now().Add(time.Duration(b.InSeconds) * time.Second)
	default:
		return j, errors.New("say when: at (a time) or inSeconds")
	}
	if b.Macro != "" {
		m, err := s.MacroStore.ByName(b.Macro)
		if err != nil || m == nil {
			return j, fmt.Errorf("no macro named %q", b.Macro)
		}
		if key != nil && !key.Has("macros.run") {
			return j, errors.New("this key may not run macros")
		}
		j.Macro = m.Name
		if j.Label == "" {
			j.Label = "Macro “" + m.Name + "”"
		}
		return j, nil
	}
	name := j.Label
	if name == "" {
		name = "Scheduled action"
	}
	m, err := macros.Validate(map[string]any{"name": name, "steps": b.Steps}, s.macroEnv())
	if err != nil {
		return j, err
	}
	for _, st := range m.Steps {
		if sc := stepScope(st); key != nil && sc != "" && !key.Has(sc) {
			return j, fmt.Errorf("this key may not schedule %q steps (needs the %s permission)", st.Type, sc)
		}
	}
	j.Steps = m.Steps
	if j.Label == "" {
		var parts []string
		for _, st := range m.Steps {
			parts = append(parts, macros.Describe(st))
		}
		j.Label = strings.Join(parts, ", then ")
	}
	return j, nil
}

func (s *Server) addSchedule(w http.ResponseWriter, r *http.Request) {
	var b scheduleBody
	if !decode(w, r, &b) {
		return
	}
	key, _ := r.Context().Value(keyCtx).(*auth.Key)
	j, err := s.buildJob(b, key)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	j.By = whoAsked(r, strings.TrimSpace(b.By))
	j, err = s.Schedule.Add(j)
	if s.scheduleErr(w, err) {
		return
	}
	writeJSON(w, 201, j)
}

// ScheduleRunner runs a due job through the macro engine.
func (s *Server) ScheduleRunner() schedule.Runner {
	return func(ctx context.Context, j schedule.Job) (*macros.Report, error) {
		if j.Macro != "" {
			m, err := s.MacroStore.ByName(j.Macro)
			if err != nil || m == nil {
				return nil, fmt.Errorf("macro %q no longer exists", j.Macro)
			}
			return s.Macros.Run(ctx, m.ID)
		}
		return s.Macros.RunSteps(ctx, 0, j.Label, j.Steps)
	}
}
