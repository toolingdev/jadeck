package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"ptz/internal/auth"
)

type ctxKey int

const keyCtx ctxKey = 1

// KeyFrom returns the API key a request authenticated with (nil when
// it came in on a trusted network without a key).
func KeyFrom(ctx context.Context) *auth.Key {
	k, _ := ctx.Value(keyCtx).(*auth.Key)
	return k
}

const (
	scopeAny     = "*"     // any valid key
	scopeTrusted = "trust" // trusted network only, never a key (key admin)
	scopePublic  = ""      // no data: pages, static files
)

// scopeFor maps a route to the permission it needs.
func scopeFor(method, path string) string {
	read := method == http.MethodGet
	has := func(p string) bool { return strings.HasPrefix(path, p) }
	switch {
	case path == "/" || path == "/dashboard" || has("/dashboard/") || path == "/settings" || has("/settings/") || path == "/setup" || has("/static/") || has("/assets/") ||
		has("/legacy") || has("/api/kit/") || path == "/api/version" || path == "/sw.js" || path == "/manifest.webmanifest" || path == "/icon.svg":
		return scopePublic
	case (has("/api/connections/") && (strings.HasSuffix(path, "/state") || strings.Contains(path, "/actions/") ||
		(read && strings.Contains(path, "/res/")))):
		return "addons" // add-on state, its pictures and actions (a capability's own routes keep their scopes)
	case has("/api/addons/") && strings.Contains(path, "/ui/") && read:
		return scopeAny // frame widgets' files: as readable as the dashboards that show them
	case (path == "/api/connections" || path == "/api/addons" || path == "/api/feeds") && read:
		return scopeAny // lists for widget pickers and previews; secrets are masked
	case path == "/api/feeds" || has("/api/feeds/"):
		return "feeds" // add, change, remove feeds (an add-on: only its own)
	case has("/api/keys") || has("/api/audit") || has("/api/connections") || has("/api/addons") || has("/api/feeds") ||
		has("/api/video") || has("/api/system") || has("/api/cameras/"):
		return scopeTrusted
	case path == "/api/cameras":
		if read {
			return "camera.read"
		}
		return scopeTrusted
	case path == "/api/setup":
		if read {
			return scopeAny
		}
		return scopeTrusted
	case has("/mcp"):
		return scopeAny // tool-level scopes apply inside
	case path == "/api/remote/me":
		return scopeAny // who am I: "not remote" on this door
	case path == "/api/config" || path == "/api/devices" || path == "/api/update/status":
		return scopeAny
	case path == "/api/stage/channels" || (has("/api/stage/channels/") && !strings.Contains(strings.TrimPrefix(path, "/api/stage/channels/"), "/")):
		return scopeTrusted // create, rename, delete channels
	case path == "/api/schedule" || has("/api/schedule/"):
		return "schedule"
	case path == "/api/stage" || has("/api/stage/"):
		return "stage"
	case has("/api/dashboards"):
		if read {
			return scopeAny // a dashboard is UI layout, not device control
		}
		return scopeTrusted
	case has("/api/update/"):
		return "update.apply"
	case path == "/api/status" || path == "/api/snapshot":
		return "camera.read"
	case has("/ws/control"):
		return "camera.move"
	case has("/api/presets"):
		switch {
		case read:
			return "camera.read"
		case strings.HasSuffix(path, "/recall"):
			return "camera.move"
		}
		return "presets.edit"
	case has("/api/ptz/") || has("/api/focus"):
		return "camera.move"
	case (has("/api/camera/image") || path == "/api/camera/controls") && read:
		return "camera.read"
	case path == "/api/camera/osd" || path == "/api/camera/focus-lock":
		return "camera.move" // drive-like: the menu cursor, the focus
	case path == "/api/camera/controls":
		return "camera.move" // speeds and Motion Sync
	case has("/api/camera/"):
		return "camera.image"
	case has("/api/switcher/") || has("/api/vmix/"):
		return "switcher"
	case has("/api/presentation/") || has("/api/proclaim/"):
		return "slides"
	case has("/api/mixer/") || has("/api/wing/"):
		return "mixer"
	case has("/api/macros"):
		if read || strings.HasSuffix(path, "/run") {
			return "macros.run"
		}
		return "macros.edit"
	}
	return scopeTrusted // anything unmapped: no key may reach it
}

func clientIP(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if h := r.Header.Get("X-API-Key"); h != "" {
		return strings.TrimSpace(h)
	}
	return ""
}

// access enforces the policy in package auth.
func (s *Server) access(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		need := scopeFor(r.Method, r.URL.Path)
		if need == scopePublic || s.Trust == nil {
			next.ServeHTTP(w, r)
			return
		}
		secret := bearer(r)
		trusted := s.Trust.Trusted(r.RemoteAddr)
		mcp := strings.HasPrefix(r.URL.Path, "/mcp")
		if secret == "" {
			if trusted && !mcp {
				next.ServeHTTP(w, r) // venue network: no key needed
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="ptz"`)
			if mcp {
				fail(w, 401, "MCP needs an API key: Authorization: Bearer ptz_…")
			} else {
				fail(w, 401, "outside the trusted network an API key is required")
			}
			return
		}
		if need == scopeTrusted {
			fail(w, 403, "this needs the trusted network (this PC, the LAN or Tailscale), not a key")
			return
		}
		if g := s.grantFor(secret); g != nil { // a Controller add-on's connection
			if need != scopeAny && !g.Has(need) {
				if r.Method != http.MethodGet && !mcp {
					s.Keys.Audit(g, clientIP(r), r.Method+" "+r.URL.Path, "denied: needs "+need)
				}
				fail(w, 403, "this add-on connection does not have the "+need+" permission")
				return
			}
			if r.Method != http.MethodGet && !mcp {
				sw := &statusWriter{ResponseWriter: w, code: 200}
				next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), keyCtx, g)))
				s.Keys.Audit(g, clientIP(r), r.Method+" "+r.URL.Path, http.StatusText(sw.code))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyCtx, g)))
			return
		}
		k, err := s.Keys.Lookup(secret)
		if errors.Is(err, auth.ErrNotFound) {
			fail(w, 401, "unknown API key")
			return
		}
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		if k.RevokedAt != nil {
			fail(w, 401, "this API key was revoked")
			return
		}
		if !k.Enabled {
			fail(w, 401, "this API key is turned off")
			return
		}
		if need != scopeAny && !k.Has(need) {
			s.Keys.Audit(k, clientIP(r), r.Method+" "+r.URL.Path, "denied: needs "+need)
			fail(w, 403, "this key does not have the "+need+" permission")
			return
		}
		s.Keys.Touch(k, clientIP(r))
		if r.Method != http.MethodGet && !mcp {
			sw := &statusWriter{ResponseWriter: w, code: 200}
			next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), keyCtx, k)))
			s.Keys.Audit(k, clientIP(r), r.Method+" "+r.URL.Path, http.StatusText(sw.code))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyCtx, k)))
	})
}

// ------------------------------------------------------- key admin

func (s *Server) keyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/keys", func(w http.ResponseWriter, r *http.Request) {
		l, err := s.Keys.List()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"keys": l, "scopes": auth.Scopes})
	})
	mux.HandleFunc("POST /api/keys", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if !decode(w, r, &b) {
			return
		}
		k, secret, err := s.Keys.Create(b.Name, b.Scopes)
		if err != nil {
			keyErr(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"key": k, "secret": secret,
			"note": "Copy this key now — it is shown only once."})
	})
	mux.HandleFunc("PATCH /api/keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var b struct {
			Name    *string   `json:"name"`
			Scopes  *[]string `json:"scopes"`
			Enabled *bool     `json:"enabled"`
		}
		if !decode(w, r, &b) {
			return
		}
		var sc []string
		if b.Scopes != nil {
			sc = *b.Scopes
		}
		k, err := s.Keys.Update(id, b.Name, sc, b.Scopes != nil, b.Enabled)
		if err != nil {
			keyErr(w, err)
			return
		}
		writeJSON(w, 200, k)
	})
	mux.HandleFunc("POST /api/keys/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		k, err := s.Keys.Revoke(id)
		if err != nil {
			keyErr(w, err)
			return
		}
		writeJSON(w, 200, k)
	})
	mux.HandleFunc("DELETE /api/keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := s.Keys.Delete(id); err != nil {
			keyErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/audit", func(w http.ResponseWriter, r *http.Request) {
		l, err := s.Keys.AuditLog(200)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, l)
	})
}

func keyErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrNotFound):
		fail(w, 404, "key not found")
	case errors.Is(err, auth.ErrInput):
		fail(w, 400, strings.TrimPrefix(err.Error(), auth.ErrInput.Error()+": "))
	default:
		fail(w, 500, err.Error())
	}
}
