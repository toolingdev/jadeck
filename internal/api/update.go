package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"ptz/internal/update"
)

// Restarter tells the API how this process is supervised.
type Restarter struct {
	Mode       string // "service" or "exec"
	HealthURL  string
	ExecArgs   []string
	ServiceArg []string
	Exit       func() // exec mode: called shortly after the helper launches
}

func (s *Server) updateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.Updates.Status())
	})
	mux.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		s.Updates.Check(ctx)
		writeJSON(w, 200, s.Updates.Status())
	})
	mux.HandleFunc("POST /api/update/dismiss", func(w http.ResponseWriter, r *http.Request) {
		s.Updates.DismissOutcome()
		writeJSON(w, 200, s.Updates.Status())
	})
	mux.HandleFunc("POST /api/update/apply", s.applyUpdate)
}

func (s *Server) applyUpdate(w http.ResponseWriter, r *http.Request) {
	ver, code, err := s.startUpdate()
	if err != nil {
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "state": "applying", "version": ver})
	s.exitForUpdate()
}

func (s *Server) exitForUpdate() {
	if rs := s.Restart; rs.Mode == "exec" && rs.Exit != nil {
		go func() { time.Sleep(700 * time.Millisecond); rs.Exit() }()
	}
}

// startUpdate hands a staged, verified release to the swap helper —
// never while something is moving.
func (s *Server) startUpdate() (string, int, error) {
	driving, op := s.Cams.Busy()
	switch {
	case driving:
		return "", 409, errors.New("the camera is being driven right now — update once it stops")
	case op:
		return "", 409, errors.New("a camera operation is in progress — try again in a few seconds")
	case s.Macros.Status()["running"] == true:
		return "", 409, errors.New("a macro is running — update once it finishes")
	}
	if st := s.Updates.Status(); st.State != "available" {
		return "", 409, errors.New("no verified update is staged")
	}
	rs := s.Restart
	if s.Port != nil {
		rs.HealthURL = fmt.Sprintf("http://127.0.0.1:%d/api/version", s.Port())
	}
	ver, err := s.Updates.Apply(update.ApplyRequest{Mode: rs.Mode, HealthURL: rs.HealthURL,
		ExecArgs: rs.ExecArgs, ServiceArg: rs.ServiceArg})
	if err != nil {
		return "", 409, err
	}
	return ver, 200, nil
}

// QuietTime is when automatic updates are allowed: never on Saturday
// 7 AM-6 PM (service day).
func QuietTime(t time.Time) bool {
	return !(t.Weekday() == time.Saturday && t.Hour() >= 7 && t.Hour() < 18)
}

// AutoUpdate installs staged updates by itself when Settings allow it.
func (s *Server) AutoUpdate(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
		if !s.Settings.Get().System.AutoUpdate || !QuietTime(time.Now()) || s.Updates.Status().State != "available" {
			continue
		}
		if ver, _, err := s.startUpdate(); err == nil {
			s.Log.Info("installing update automatically", "version", ver)
			s.exitForUpdate()
			return
		}
	}
}
