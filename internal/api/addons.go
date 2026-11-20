package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"ptz/internal/addons"
	"ptz/internal/devices"
	"ptz/internal/update"

	"github.com/toolingdev/ptz-controller/sdk/addon/pack"
)

// Settings → Add-ons: what's installed, what each adds (with previews),
// turn on/off, install from a file, per-add-on updates and roll back,
// and dev folders for people writing add-ons.

func (s *Server) addonRoutes(mux *http.ServeMux) {
	s.catalogRoutes(mux)
	mux.HandleFunc("GET /api/addons", s.listAddons)
	mux.HandleFunc("POST /api/addons/install", s.installAddon)
	mux.HandleFunc("POST /api/addons/check", s.checkAddonUpdates)
	mux.HandleFunc("POST /api/addons/dev", s.loadDevAddon)
	mux.HandleFunc("DELETE /api/addons/dev/{id}", s.unloadDevAddon)
	mux.HandleFunc("POST /api/addons/{id}/enabled", s.enableAddon)
	mux.HandleFunc("POST /api/addons/{id}/restart", s.addonDo(func(id string) error { s.Addons.Restart(id); return nil }, "restarted"))
	mux.HandleFunc("POST /api/addons/{id}/rollback", s.addonDo(s.Addons.RollBack, "rolled back"))
	mux.HandleFunc("POST /api/addons/{id}/update", s.updateAddon)
	mux.HandleFunc("DELETE /api/addons/{id}", s.addonDo(s.Addons.Remove, "removed"))
	mux.HandleFunc("GET /api/addons/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		entries := s.Logs.Query(0, slog.LevelDebug, "addon="+r.PathValue("id"), 200)
		writeJSON(w, 200, map[string]any{"entries": entries})
	})
}

// addon update offers, from the last check
type addonOffer struct {
	Version string `json:"version"`
	Notes   string `json:"notes,omitempty"`
	url     string
}

var (
	offersMu  sync.Mutex
	offers    = map[string]addonOffer{}
	offersErr string
	offersAt  time.Time
)

func (s *Server) listAddons(w http.ResponseWriter, r *http.Request) {
	if s.Addons == nil {
		writeJSON(w, 200, map[string]any{"addons": []any{}})
		return
	}
	set := s.Devs()
	// where each add-on is used
	connsOf := map[string][]string{}
	for _, l := range set.Conns {
		connsOf[l.Addon] = append(connsOf[l.Addon], l.ID)
	}
	widgetsOf, dashOf := map[string]int{}, map[string]map[int64]bool{}
	if s.Dashboards != nil {
		if list, err := s.Dashboards.List(); err == nil {
			for _, d := range list {
				for _, wd := range d.Widgets {
					conn, _ := wd.Settings["connection"].(string)
					for aid, ids := range connsOf {
						hit := strings.HasPrefix(wd.Type, "addon:"+aid+":")
						for _, id := range ids {
							hit = hit || conn == id
						}
						if hit {
							widgetsOf[aid]++
							if dashOf[aid] == nil {
								dashOf[aid] = map[int64]bool{}
							}
							dashOf[aid][d.ID] = true
						}
					}
				}
			}
		}
	}
	offersMu.Lock()
	defer offersMu.Unlock()
	out := []map[string]any{}
	for _, in := range s.Addons.List() {
		id := in.Manifest.ID
		v := map[string]any{"info": in, "connections": len(connsOf[id]), "widgets": widgetsOf[id], "dashboards": len(dashOf[id])}
		if o, ok := offers[id]; ok && addons.Newer(o.Version, in.Manifest.Version) {
			v["update"] = o
		}
		out = append(out, v)
	}
	res := map[string]any{"addons": out, "platform": pack.Platform(), "checkError": offersErr}
	if !offersAt.IsZero() {
		res["checkedAt"] = offersAt
	}
	writeJSON(w, 200, res)
}

// reopen rebuilds the live connections (after an add-on changed).
func (s *Server) reopenConnections() {
	f := s.currentFile()
	if old := s.Devs(); old != nil {
		old.Close()
	}
	set := devices.Open(f, s.Addons.Open)
	s.devMu.Lock()
	s.Dev, s.DevFile = set, f
	s.devMu.Unlock()
	s.pruneGrants(f)
}

// OpenControllers reopens the connections once the server can grant
// access: at startup the set opens before the server exists, so a
// Controller add-on's connection (the AI) would start without its token.
func (s *Server) OpenControllers() {
	f := s.currentFile()
	for _, c := range f.Connections {
		if m, ok := s.Addons.Manifest(c.Addon); ok && m.Controller && !c.Disabled {
			s.reopenConnections()
			return
		}
	}
}

func (s *Server) addonDo(do func(id string) error, what string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := do(id); err != nil {
			fail(w, 409, err.Error())
			return
		}
		s.reopenConnections()
		s.Log.Info("add-on "+what, "addon", id, "client", clientIP(r))
		writeJSON(w, 200, map[string]any{"ok": true})
	}
}

func (s *Server) enableAddon(w http.ResponseWriter, r *http.Request) {
	var b struct {
		On bool `json:"on"`
	}
	if !decode(w, r, &b) {
		return
	}
	id := r.PathValue("id")
	if err := s.Addons.SetEnabled(id, b.On); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.reopenConnections()
	s.Log.Info("add-on turned "+map[bool]string{true: "on", false: "off"}[b.On], "addon", id, "client", clientIP(r))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// installAddon takes a .ptzaddon as the request body. An unverified
// package answers 409 {unverified: true, manifest} until the person
// confirms with ?allowUnverified=1.
func (s *Server) installAddon(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 200<<20+1))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	p, err := pack.Read(data, s.Addons.PubKey)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	allow := r.URL.Query().Get("allowUnverified") == "1"
	if !p.Verified && !allow {
		writeJSON(w, 409, map[string]any{"unverified": true, "manifest": p.Manifest,
			"detail": "This add-on is not signed by the PTZ Controller release key. Install it only if you trust where it came from."})
		return
	}
	if err := s.Addons.Install(p, allow); err != nil {
		fail(w, 422, err.Error())
		return
	}
	s.reopenConnections()
	s.Log.Info("add-on installed from a file", "addon", p.Manifest.ID, "version", p.Manifest.Version, "verified", p.Verified, "client", clientIP(r))
	writeJSON(w, 200, map[string]any{"ok": true, "manifest": p.Manifest, "verified": p.Verified})
}

// checkAddonUpdates asks each add-on's repository for its latest release.
func (s *Server) checkAddonUpdates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	s.CheckAddonUpdates(ctx)
	s.listAddons(w, r)
}

// CheckAddonUpdates refreshes the update offers (also run daily).
func (s *Server) CheckAddonUpdates(ctx context.Context) {
	if s.Addons == nil {
		return
	}
	found := map[string]addonOffer{}
	var errs []string
	token := s.Settings.Get().System.UpdateToken
	for _, in := range s.Addons.List() {
		m := in.Manifest
		if m.Repo == "" || in.Source == "dev" {
			continue
		}
		src := &update.Source{Repo: m.Repo, Token: token}
		rel, err := src.Latest(ctx)
		if err != nil {
			errs = append(errs, m.Name+": "+err.Error())
			continue
		}
		for _, a := range rel.Assets {
			if a.Name == m.ID+".ptzaddon" {
				found[m.ID] = addonOffer{Version: strings.TrimPrefix(rel.Tag, "v"), Notes: rel.Body, url: a.URL}
			}
		}
	}
	offersMu.Lock()
	offers, offersAt, offersErr = found, time.Now(), strings.Join(errs, "; ")
	offersMu.Unlock()
}

func (s *Server) updateAddon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	offersMu.Lock()
	o, ok := offers[id]
	offersMu.Unlock()
	m, has := s.Addons.Manifest(id)
	if !ok || !has || !addons.Newer(o.Version, m.Version) {
		fail(w, 409, "no newer version known — check for updates first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	src := &update.Source{Repo: m.Repo, Token: s.Settings.Get().System.UpdateToken}
	data, err := src.Download(ctx, update.Asset{Name: id + ".ptzaddon", URL: o.url}, 200<<20)
	if err != nil {
		fail(w, 502, "download failed: "+err.Error())
		return
	}
	p, err := pack.Read(data, s.Addons.PubKey)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	if p.Manifest.ID != id {
		fail(w, 422, fmt.Sprintf("the download is add-on %q, not %q", p.Manifest.ID, id))
		return
	}
	if !p.Verified {
		fail(w, 422, "the update is not signed by the release key — not installed")
		return
	}
	if err := s.Addons.Install(p, false); err != nil {
		fail(w, 422, err.Error())
		return
	}
	s.reopenConnections()
	s.Log.Info("add-on updated", "addon", id, "from", m.Version, "to", p.Manifest.Version, "client", clientIP(r))
	writeJSON(w, 200, map[string]any{"ok": true, "version": p.Manifest.Version})
}

func (s *Server) loadDevAddon(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Path    string   `json:"path"`
		Command []string `json:"command"`
	}
	if !decode(w, r, &b) {
		return
	}
	if strings.TrimSpace(b.Path) == "" {
		fail(w, 422, "path is required")
		return
	}
	m, err := s.Addons.LoadDev(b.Path, b.Command)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	s.reopenConnections()
	s.Log.Info("dev add-on loaded", "addon", m.ID, "path", b.Path, "client", clientIP(r))
	writeJSON(w, 200, map[string]any{"ok": true, "manifest": m})
}

func (s *Server) unloadDevAddon(w http.ResponseWriter, r *http.Request) {
	if err := s.Addons.UnloadDev(r.PathValue("id")); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.reopenConnections()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ------------------------------------------------------------ catalog

type catalogEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Publisher   string `json:"publisher"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// catalogURL is the list Browse shows: addons/catalog.json on the main
// branch of the repository updates come from.
func (s *Server) catalogURL() string {
	repo := s.Settings.Get().System.UpdateRepo
	if repo == "" {
		repo = "toolingdev/ptz-controller"
	}
	return "https://raw.githubusercontent.com/" + repo + "/main/addons/catalog.json"
}

func (s *Server) fetchCatalog(ctx context.Context) ([]catalogEntry, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", s.catalogURL(), nil)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't load the add-on list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("the add-on list answered %d", resp.StatusCode)
	}
	var c struct {
		Addons []catalogEntry `json:"addons"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&c); err != nil {
		return nil, fmt.Errorf("the add-on list is unreadable: %w", err)
	}
	return c.Addons, nil
}

func (s *Server) catalogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/addons/catalog", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		list, err := s.fetchCatalog(ctx)
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		out := []map[string]any{}
		for _, e := range list {
			v := map[string]any{"entry": e}
			if m, ok := s.Addons.Manifest(e.ID); ok {
				v["installed"] = m.Version
			}
			out = append(out, v)
		}
		writeJSON(w, 200, map[string]any{"addons": out, "source": s.catalogURL()})
	})
	mux.HandleFunc("POST /api/addons/catalog/{id}/install", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		id := r.PathValue("id")
		list, err := s.fetchCatalog(ctx)
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		var e *catalogEntry
		for i := range list {
			if list[i].ID == id {
				e = &list[i]
			}
		}
		if e == nil {
			fail(w, 404, "that add-on isn't in the list")
			return
		}
		src := &update.Source{Repo: e.Repo, Token: s.Settings.Get().System.UpdateToken}
		rel, err := src.Latest(ctx)
		if err != nil {
			fail(w, 502, e.Name+": "+err.Error())
			return
		}
		var asset *update.Asset
		for i := range rel.Assets {
			if rel.Assets[i].Name == id+".ptzaddon" {
				asset = &rel.Assets[i]
			}
		}
		if asset == nil {
			fail(w, 502, fmt.Sprintf("%s %s has no %s.ptzaddon", e.Name, rel.Tag, id))
			return
		}
		data, err := src.Download(ctx, *asset, 200<<20)
		if err != nil {
			fail(w, 502, "download failed: "+err.Error())
			return
		}
		p, err := pack.Read(data, s.Addons.PubKey)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		if p.Manifest.ID != id {
			fail(w, 422, fmt.Sprintf("the download is add-on %q, not %q", p.Manifest.ID, id))
			return
		}
		allow := r.URL.Query().Get("allowUnverified") == "1"
		if !p.Verified && !allow {
			writeJSON(w, 409, map[string]any{"unverified": true, "manifest": p.Manifest,
				"detail": "This add-on is not signed by the PTZ Controller release key. Install it only if you trust " + e.Publisher + "."})
			return
		}
		if err := s.Addons.Install(p, allow); err != nil {
			fail(w, 422, err.Error())
			return
		}
		s.reopenConnections()
		s.Log.Info("add-on installed from Browse", "addon", id, "version", p.Manifest.Version, "verified", p.Verified, "client", clientIP(r))
		writeJSON(w, 200, map[string]any{"ok": true, "manifest": p.Manifest, "verified": p.Verified})
	})
}
