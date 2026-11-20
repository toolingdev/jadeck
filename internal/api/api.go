// Package api is the HTTP + WebSocket surface. Routes and JSON shapes
// match the Python build, so the existing web UI runs unchanged.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"ptz/internal/auth"
	"ptz/internal/camera"
	"ptz/internal/cams"
	"ptz/internal/dashboards"
	"ptz/internal/devices"
	"ptz/internal/imageconf"
	"ptz/internal/logx"
	"ptz/internal/macros"
	"ptz/internal/netinfo"
	"ptz/internal/settings"
	"ptz/internal/stage"
	"ptz/internal/update"
	"ptz/internal/video"
)

type Server struct {
	Cams       *cams.Registry
	Settings   *settings.Store
	Video      *video.Gateway
	Mock       bool // simulated cameras answer the network scan
	Names      *netinfo.Responder
	BindHost   string
	PortLocked bool       // PTZ_PORT set on this PC: Settings can't change it
	Port       func() int // port being served now
	Web        fs.FS      // new app (React build)
	Legacy     fs.FS      // previous UI, served at /legacy
	Log        *slog.Logger
	Version    string

	Dev         *devices.Set  // initial device set; use Devs() — it can be swapped live
	DevFile     *devices.File // the config Dev was built from
	DevicesPath string        // where the Settings screen saves it
	devMu       sync.RWMutex
	MacroStore  *macros.Store
	Macros      *macros.Engine
	Updates     *update.Manager
	Restart     Restarter
	Keys        *auth.Store
	Dashboards  *dashboards.Store
	Trust       *auth.Trust
	Stage       *stage.Store
	Logs        *logx.Logs // diagnostics: log ring, files, level

	hub    *hub
	mux    *http.ServeMux
	health health
}

func (s *Server) Handler() http.Handler {
	s.hub = newHub()
	s.Cams.OnStatus = func(id string, st camera.Status) {
		s.hub.broadcast(id, statusMsg(id, st))
		name := id
		if u := s.Cams.Get(id); u != nil {
			name = u.Cfg.Name
		}
		s.track("camera "+name, st.Connected, st.Error)
	}

	mux := http.NewServeMux()
	s.mux = mux
	mux.HandleFunc("GET /api/config", s.config)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/snapshot", s.snapshot)
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"version": s.Version})
	})

	mux.HandleFunc("GET /api/presets", s.listPresets)
	mux.HandleFunc("POST /api/presets", s.createPreset)
	mux.HandleFunc("POST /api/presets/reorder", s.reorderPresets)
	mux.HandleFunc("PATCH /api/presets/{id}", s.patchPreset)
	mux.HandleFunc("DELETE /api/presets/{id}", s.deletePreset)
	mux.HandleFunc("POST /api/presets/{id}/recall", s.recallPreset)
	mux.HandleFunc("POST /api/presets/{id}/store", s.storePreset)

	mux.HandleFunc("POST /api/ptz/home", s.home)
	mux.HandleFunc("POST /api/ptz/stop", s.stop)
	mux.HandleFunc("POST /api/focus", s.focus)
	mux.HandleFunc("POST /api/camera/wb-pick", s.wbPick)
	mux.HandleFunc("GET /api/camera/image", s.getImage)
	mux.HandleFunc("GET /api/camera/image/fields", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, imageconf.Specs())
	})
	mux.HandleFunc("POST /api/camera/image", s.postImage)
	mux.HandleFunc("POST /api/camera/image/wb-trigger", s.wbTriggerCGI)

	s.setupRoutes(mux)
	s.deviceRoutes(mux)
	s.devConfigRoutes(mux)
	if s.Dashboards != nil {
		s.dashboardRoutes(mux)
	}
	s.macroRoutes(mux)
	if s.Stage != nil {
		s.stageRoutes(mux)
	}
	if s.Logs != nil {
		s.diagRoutes(mux)
	}
	s.updateRoutes(mux)
	if s.Keys != nil {
		s.keyRoutes(mux)
		s.mcpRoutes(mux)
	}

	mux.HandleFunc("GET /ws/control", s.ws)

	// New app: one index.html for every client-side route.
	for _, p := range []string{"GET /{$}", "GET /dashboard", "GET /dashboard/{rest...}", "GET /settings", "GET /settings/{rest...}", "GET /setup"} {
		mux.HandleFunc(p, s.page(s.Web, "index.html"))
	}
	mux.Handle("GET /assets/", immutable(http.FileServerFS(s.Web)))
	for _, f := range []string{"sw.js", "manifest.webmanifest", "icon.svg"} {
		mux.Handle("GET /"+f, noCache(http.FileServerFS(s.Web)))
	}
	if s.Legacy != nil {
		mux.HandleFunc("GET /legacy", s.page(s.Legacy, "index.html"))
		mux.HandleFunc("GET /legacy/dashboard", s.page(s.Legacy, "dashboard.html"))
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(s.Legacy)))
	}
	return logRequests(s.Log, s.access(mux))
}

// ------------------------------------------------------------ helpers

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"detail": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, 422, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 422, "id must be an integer")
		return 0, false
	}
	return id, true
}

func immutable(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // hashed file names
		h.ServeHTTP(w, r)
	})
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) page(fsys fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	}
}

type statusWriter struct {
	http.ResponseWriter
	code int
	body []byte // first bytes of an error answer, for the log
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code >= 400 && len(w.body) < 300 {
		w.body = append(w.body, b[:min(len(b), 300-len(w.body))]...)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets the WebSocket upgrader and event streams reach the real writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests logs every change (non-GET) and every failed read, with
// the reason the server gave, and turns a panic into a logged 500
// instead of a dropped connection.
func logRequests(log *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: 200}
		t := time.Now()
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				log.Error("panic serving request", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
				if sw.code == 200 {
					fail(sw, 500, "internal error (logged)")
				}
				return
			}
			api := strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/mcp")
			if !api || r.URL.Path == "/api/diag/client" {
				return
			}
			if r.Method == http.MethodGet && sw.code < 400 {
				return
			}
			attrs := []any{"method", r.Method, "path", r.URL.Path, "code", sw.code, "ms", time.Since(t).Milliseconds(), "client", clientIP(r)}
			if c := r.Header.Get("X-PTZ-Camera"); c != "" {
				attrs = append(attrs, "camera", c)
			}
			if sw.code >= 400 {
				var e struct{ Detail string }
				if json.Unmarshal(sw.body, &e) == nil && e.Detail != "" {
					attrs = append(attrs, "reason", e.Detail)
				}
			}
			switch {
			case sw.code >= 500:
				log.Error("api", attrs...)
			case sw.code >= 400:
				log.Warn("api", attrs...)
			default:
				log.Info("api", attrs...)
			}
		}()
		h.ServeHTTP(sw, r)
	})
}

// ---------------------------------------------------------- endpoints

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-PTZ-Camera")
	if id == "" {
		id = r.URL.Query().Get("cam")
	}
	u := s.Cams.Get(id)
	if u == nil {
		u = s.Cams.Main()
	}
	writeJSON(w, 200, s.streamURLs(r, u))
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	u.Svc.RefreshStatic(ctx)
	writeJSON(w, 200, u.Svc.Snapshot())
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	b, err := u.Svc.FetchSnapshot(r.Context())
	if err != nil || len(b) == 0 {
		fail(w, 502, "snapshot unavailable")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (s *Server) listPresets(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	l, err := u.Presets.List()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, l)
}

func (s *Server) createPreset(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	var b struct {
		Name string `json:"name"`
		Slot *int   `json:"slot"`
	}
	if !decode(w, r, &b) {
		return
	}
	name := strings.TrimSpace(b.Name)
	if len(b.Name) < 1 || len(b.Name) > 60 || name == "" {
		fail(w, 422, "name must be 1-60 characters")
		return
	}
	slot := 0
	if b.Slot != nil {
		if *b.Slot < 1 || *b.Slot > 127 {
			fail(w, 422, "slot must be 1-127")
			return
		}
		slot = *b.Slot
	}
	p, err := u.Presets.Create(name, slot)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) patchPreset(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Name        *string        `json:"name"`
		Exposure    map[string]any `json:"exposure"`
		SetExposure bool           `json:"setExposure"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Name != nil {
		if len(*b.Name) < 1 || len(*b.Name) > 60 {
			fail(w, 422, "name must be 1-60 characters")
			return
		}
		n := strings.TrimSpace(*b.Name)
		b.Name = &n
	}
	p, err := u.Presets.Update(id, b.Name, b.Exposure, b.SetExposure)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if p == nil {
		fail(w, 404, "preset not found")
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) deletePreset(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	found, err := u.Presets.Delete(id)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if !found {
		fail(w, 404, "preset not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) reorderPresets(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	var b struct {
		IDs []int64 `json:"ids"`
	}
	if !decode(w, r, &b) {
		return
	}
	l, err := u.Presets.Reorder(b.IDs)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, l)
}

// motionGuard rejects discrete motion when disconnected (503) or while
// a sequenced operation owns the camera (409, never queued).
func (s *Server) motionGuard(w http.ResponseWriter, u *cams.Unit) bool {
	if !u.Svc.Connected() {
		fail(w, 503, "camera not connected")
		return false
	}
	if u.Svc.OperationActive() {
		fail(w, 409, "camera is busy (white-balance pick in progress)")
		return false
	}
	return true
}

func (s *Server) presetMotion(w http.ResponseWriter, r *http.Request, do func(*cams.Unit, int) error) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := u.Presets.Get(id)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if p == nil {
		fail(w, 404, "preset not found")
		return
	}
	if !s.motionGuard(w, u) {
		return
	}
	if err := do(u, p.Slot); err != nil {
		fail(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "slot": p.Slot})
}

func (s *Server) recallPreset(w http.ResponseWriter, r *http.Request) {
	s.presetMotion(w, r, func(u *cams.Unit, slot int) error { return u.Svc.RecallPreset(slot) })
}

func (s *Server) storePreset(w http.ResponseWriter, r *http.Request) {
	s.presetMotion(w, r, func(u *cams.Unit, slot int) error { return u.Svc.StorePreset(slot) })
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	if !s.motionGuard(w, u) {
		return
	}
	if err := u.Svc.Home(); err != nil {
		fail(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	u.Svc.ForceStop()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) focus(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	var b struct {
		Action string `json:"action"`
	}
	if !decode(w, r, &b) {
		return
	}
	switch b.Action {
	case "auto", "manual", "onepush", "near", "far", "stop":
	default:
		fail(w, 400, "unknown focus action")
		return
	}
	if !s.motionGuard(w, u) {
		return
	}
	if err := u.Svc.Focus(b.Action); err != nil {
		fail(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) wbPick(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	var b struct {
		X *float64 `json:"x"`
		Y *float64 `json:"y"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.X == nil || b.Y == nil || *b.X < 0 || *b.X > 1 || *b.Y < 0 || *b.Y > 1 {
		fail(w, 422, "x and y must be between 0 and 1")
		return
	}
	if !u.Svc.Connected() {
		fail(w, 503, "camera not connected")
		return
	}
	res, err := u.Svc.WBPick(*b.X, *b.Y)
	switch {
	case errors.Is(err, camera.ErrBusy):
		fail(w, 409, err.Error())
	case err != nil:
		fail(w, 502, err.Error())
	default:
		writeJSON(w, 200, res)
	}
}

func (s *Server) fetchConf(ctx context.Context, u *cams.Unit) (*imageconf.Conf, error) {
	b, err := u.Svc.HTTPGet(ctx, "/cgi-bin/param.cgi?get_image_conf")
	if err != nil {
		return nil, err
	}
	return imageconf.Parse(string(b)), nil
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	c, err := s.fetchConf(r.Context(), u)
	if err != nil {
		fail(w, 502, "camera image config unavailable")
		return
	}
	writeJSON(w, 200, imageconf.ToTyped(c))
}

func (s *Server) postImage(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	var raw map[string]any
	if !decode(w, r, &raw) {
		return
	}
	updates := map[string]any{}
	for k, v := range raw {
		if v != nil {
			updates[k] = v
		}
	}
	if len(updates) == 0 {
		fail(w, 400, "no image fields supplied")
		return
	}
	c, err := s.fetchConf(r.Context(), u)
	if err != nil {
		fail(w, 502, "camera image config unavailable")
		return
	}
	n, err := imageconf.Apply(c, updates)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if _, err := u.Svc.HTTPGet(r.Context(), "/cgi-bin/param.cgi?"+n.PostQuery()); err != nil {
		fail(w, 502, "camera rejected the image config")
		return
	}
	if back, err := s.fetchConf(r.Context(), u); err == nil {
		n = back
	}
	writeJSON(w, 200, imageconf.ToTyped(n))
}

func (s *Server) wbTriggerCGI(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	if _, err := u.Svc.HTTPGet(r.Context(), "/cgi-bin/param.cgi?post_image_value&wbmode&trigger"); err != nil {
		fail(w, 502, "white-balance trigger failed")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Devs returns the live device set (it can be replaced from Settings).
func (s *Server) Devs() *devices.Set {
	s.devMu.RLock()
	defer s.devMu.RUnlock()
	return s.Dev
}

// SetDevices swaps in a new device set and closes the old one.
func (s *Server) SetDevices(f *devices.File, set *devices.Set) {
	s.devMu.Lock()
	old := s.Dev
	s.Dev, s.DevFile = set, f
	s.devMu.Unlock()
	if old != nil && old != set {
		old.Close()
	}
}
