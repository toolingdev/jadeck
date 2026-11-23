package api

// Remote access: the door a Cloudflare Tunnel connects to (127.0.0.1 only,
// its own port). Nothing on it is trusted: every request needs Cloudflare
// Access's signed sign-in, and the email decides what the person gets:
// everything, or only their dashboards (and what those dashboards need to
// show and answer). The local network's door is untouched.

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"ptz/internal/dashboards"
	"ptz/internal/remote"
)

const remoteCtx ctxKey = 2

// RemoteFrom is the person a request came in as over the remote door
// (nil on the local network).
func RemoteFrom(ctx context.Context) *remote.Person {
	p, _ := ctx.Value(remoteCtx).(*remote.Person)
	return p
}

// viewerActions are the add-on actions someone limited to a dashboard
// may run: marking a comment addressed, nothing that sends or changes gear.
var viewerActions = []string{"comment_addressed"}

func (s *Server) remoteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/remote/me", func(w http.ResponseWriter, r *http.Request) {
		p := RemoteFrom(r.Context())
		if p == nil {
			writeJSON(w, 200, map[string]any{"remote": false})
			return
		}
		writeJSON(w, 200, map[string]any{"remote": true, "email": p.Email, "name": p.Name, "everything": p.Everything, "dashboards": p.Dashboards})
	})
	mux.HandleFunc("GET /api/remote", func(w http.ResponseWriter, r *http.Request) {
		if s.Remote == nil {
			fail(w, 404, "remote access isn't available in this build")
			return
		}
		c := s.Remote.Get()
		writeJSON(w, 200, map[string]any{"config": c, "lastRequest": s.remoteLast()})
	})
	mux.HandleFunc("PUT /api/remote", func(w http.ResponseWriter, r *http.Request) {
		if s.Remote == nil {
			fail(w, 404, "remote access isn't available in this build")
			return
		}
		var c remote.Config
		if !decode(w, r, &c) {
			return
		}
		if err := s.Remote.Set(c); err != nil {
			fail(w, 400, err.Error())
			return
		}
		c = s.Remote.Get()
		if s.OnRemote != nil {
			if err := s.OnRemote(c); err != nil {
				fail(w, 500, err.Error())
				return
			}
		}
		s.Log.Info("remote access saved", "enabled", c.Enabled, "people", len(c.People))
		writeJSON(w, 200, map[string]any{"config": c, "lastRequest": s.remoteLast()})
	})
}

func (s *Server) remoteLast() *time.Time {
	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()
	if s.remoteAt.IsZero() {
		return nil
	}
	t := s.remoteAt
	return &t
}

// RemoteHandler is what the remote listener serves.
func (s *Server) RemoteHandler() http.Handler {
	return logRequests(s.Log, http.HandlerFunc(s.remoteDoor))
}

func (s *Server) remoteDoor(w http.ResponseWriter, r *http.Request) {
	if s.Remote == nil || s.mux == nil {
		http.Error(w, "remote access isn't set up", http.StatusServiceUnavailable)
		return
	}
	c := s.Remote.Get()
	if !c.Enabled {
		http.Error(w, "Remote access to this jadeck is turned off.", http.StatusServiceUnavailable)
		return
	}
	s.remoteMu.Lock()
	s.remoteAt = time.Now()
	s.remoteMu.Unlock()
	tok := r.Header.Get("Cf-Access-Jwt-Assertion")
	if tok == "" {
		if ck, err := r.Cookie("CF_Authorization"); err == nil {
			tok = ck.Value
		}
	}
	email, err := s.RemoteVerify.Verify(r.Context(), tok, c.Team, c.AUD)
	if err != nil {
		s.Log.Warn("remote: refused", "path", r.URL.Path, "reason", err.Error())
		http.Error(w, "Sign in through the link you were sent ("+err.Error()+").", http.StatusUnauthorized)
		return
	}
	p := c.Find(email)
	if p == nil {
		s.Log.Warn("remote: not on the list", "email", email)
		http.Error(w, "This jadeck hasn't let "+email+" in. Ask the person who runs it to add you.", http.StatusForbidden)
		return
	}
	s.Remote.Seen(email, time.Now())
	// Never from outside, whoever you are: who may come in, and API keys.
	if strings.HasPrefix(r.URL.Path, "/api/remote") && r.URL.Path != "/api/remote/me" || strings.HasPrefix(r.URL.Path, "/api/keys") || strings.HasPrefix(r.URL.Path, "/api/audit") {
		fail(w, 403, "change remote access and API keys from the local network")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), remoteCtx, p))
	if !p.Everything && !s.remoteAllowed(r, p) {
		fail(w, 403, "not on your screen")
		return
	}
	s.mux.ServeHTTP(w, r)
}

// remoteAllowed is the short list of what a dashboard-only person's
// screens need: the app's files, their dashboards, stage messages on
// their dashboards' channels, and read-only state for their widgets.
func (s *Server) remoteAllowed(r *http.Request, p *remote.Person) bool {
	path := r.URL.Path
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	has := func(pre string) bool { return strings.HasPrefix(path, pre) }
	if get {
		switch {
		case scopeFor(r.Method, path) == scopePublic && !has("/legacy") && !has("/static/"):
			return true // pages, the app's files, the frame kit
		case path == "/api/remote/me", path == "/api/addons", path == "/api/devices", path == "/api/setup", path == "/api/update/status",
			path == "/api/switcher/status", path == "/api/stage", path == "/api/stage/events", path == "/api/dashboards", path == "/api/dashboards/defaults":
			return true
		case has("/api/addons/") && strings.Contains(path, "/ui/"):
			return true // add-on widget files
		case has("/api/dashboards/"):
			id, err := strconv.ParseInt(strings.TrimPrefix(path, "/api/dashboards/"), 10, 64)
			return err == nil && p.Sees(id)
		case has("/api/connections/"):
			id, rest, _ := strings.Cut(strings.TrimPrefix(path, "/api/connections/"), "/")
			return (rest == "state" || strings.HasPrefix(rest, "res/")) && s.remoteUses(p).conns[id]
		}
		return false
	}
	// clearing the conversation on his own channel (Messages → Clear, design
	// page 38); never another channel's
	if r.Method == http.MethodDelete && has("/api/stage/channels/") {
		id, rest, _ := strings.Cut(strings.TrimPrefix(path, "/api/stage/channels/"), "/")
		return rest == "messages" && s.remoteUses(p).channels[id]
	}
	if r.Method != http.MethodPost {
		return false
	}
	if has("/api/stage/channels/") {
		id, rest, _ := strings.Cut(strings.TrimPrefix(path, "/api/stage/channels/"), "/")
		return (rest == "messages" || rest == "seen" || rest == "reply") && s.remoteUses(p).channels[id]
	}
	if has("/api/connections/") {
		id, rest, _ := strings.Cut(strings.TrimPrefix(path, "/api/connections/"), "/")
		name, ok := strings.CutPrefix(rest, "actions/")
		return ok && slices.Contains(viewerActions, name) && s.remoteUses(p).conns[id]
	}
	return false
}

type uses struct {
	conns    map[string]bool
	channels map[string]bool
}

// remoteUses is what p's dashboards use: connections (named in a widget,
// or any connection of an add-on a widget is from) and stage channels.
func (s *Server) remoteUses(p *remote.Person) uses {
	u := uses{conns: map[string]bool{}, channels: map[string]bool{}}
	addons := map[string]bool{}
	for _, id := range p.Dashboards {
		d, err := s.Dashboards.Get(id)
		if err != nil {
			continue
		}
		for _, w := range d.Widgets {
			collect(w, u, addons)
		}
	}
	if len(addons) > 0 {
		for _, c := range s.currentFile().Connections {
			if addons[c.Addon] {
				u.conns[c.ID] = true
			}
		}
	}
	return u
}

func collect(w dashboards.Widget, u uses, addons map[string]bool) {
	if c, ok := w.Settings["connection"].(string); ok && c != "" {
		u.conns[c] = true
	}
	if a, ok := w.Settings["addon"].(string); ok && a != "" {
		addons[a] = true
	}
	if c, ok := w.Settings["channel"].(string); ok && c != "" {
		u.channels[c] = true
	}
	if l, ok := w.Settings["channels"].([]any); ok {
		for _, x := range l {
			if c, ok := x.(string); ok {
				u.channels[c] = true
			}
		}
	}
}

// filterDashboards keeps only what a remote person may see.
func filterDashboards(p *remote.Person, l []dashboards.Dashboard) []dashboards.Dashboard {
	if p == nil || p.Everything {
		return l
	}
	out := []dashboards.Dashboard{}
	for _, d := range l {
		if p.Sees(d.ID) {
			out = append(out, d)
		}
	}
	return out
}
