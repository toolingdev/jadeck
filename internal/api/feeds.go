package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ptz/internal/auth"
	"ptz/internal/settings"
	"ptz/internal/video"
)

// Settings → Video feeds: every picture a dashboard can show. Cameras
// bring their own feed; other feeds are sent here (SRT push, e.g. vMix
// Output 2) or pulled from an address (RTSP / SRT).

func (s *Server) feedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/feeds", s.listFeeds)
	mux.HandleFunc("POST /api/feeds", s.saveFeed)
	mux.HandleFunc("PUT /api/feeds/{id}", s.saveFeed)
	mux.HandleFunc("DELETE /api/feeds/{id}", s.deleteFeed)
	mux.HandleFunc("POST /api/feeds/{id}/key", s.newFeedKey)
}

var feedIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// Who may manage feeds: the trusted network (no key) and API keys with
// the feeds permission manage them all; a Controller add-on's
// connection with that permission manages only the feeds it added.

// feedCaller is the token behind r (nil on the trusted network).
func feedCaller(r *http.Request) *auth.Key { return KeyFrom(r.Context()) }

// canManageFeed reports whether the caller may change f (or see its key).
func canManageFeed(k *auth.Key, f settings.Feed) bool {
	switch {
	case k == nil:
		return true
	case !k.Has("feeds"):
		return false
	case k.Conn == "":
		return true // an API key
	}
	return f.Owner == k.Conn
}

// auditFeed records a feed change made with a token.
func (s *Server) auditFeed(r *http.Request, action string) {
	if k := feedCaller(r); k != nil && s.Keys != nil {
		s.Keys.Audit(k, clientIP(r), action, "ok")
	}
}

func (s *Server) feedHost(r *http.Request) string {
	set := s.Settings.Get()
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if !set.Video.Managed && set.Video.External != "" {
		host = set.Video.External
	}
	return host
}

// pushInfo is what a sender needs; the key only for callers allowed it.
func pushInfo(host string, f settings.Feed, withKey bool) map[string]any {
	p := map[string]any{"host": host, "port": video.PortSRT, "rtspPort": video.PortRTSP, "user": video.PublishUser(f.ID)}
	if !withKey || f.Key == "" {
		p["streamId"] = "publish:" + f.ID + ":" + video.PublishUser(f.ID) + ":…"
		p["keyHidden"] = true
		return p
	}
	p["key"] = f.Key
	p["streamId"] = video.SRTStreamID(f.ID, f.Key)
	p["url"] = video.SRTPublishURL(host, f.ID, f.Key)
	p["rtspUrl"] = video.RTSPPublishURL(host, f.ID, f.Key)
	return p
}

func (s *Server) feedView(r *http.Request, f settings.Feed, stats map[string]any) map[string]any {
	k := feedCaller(r)
	v := map[string]any{"id": f.ID, "name": f.Name, "source": f.Source, "url": f.URL, "enabled": true, "status": stats,
		"canEdit": canManageFeed(k, f)}
	if f.Owner != "" {
		v["owner"] = f.Owner
		if d := s.Devs(); d != nil {
			if l := d.Get(f.Owner); l != nil {
				v["ownerName"] = l.Name
			}
		}
	}
	if f.Source == "push" {
		v["push"] = pushInfo(s.feedHost(r), f, canManageFeed(k, f))
	}
	return v
}

func (s *Server) listFeeds(w http.ResponseWriter, r *http.Request) {
	set := s.Settings.Get()
	stats := map[string]map[string]any{}
	if s.Video != nil {
		for _, f := range s.Video.Stats().Feeds {
			stats[f.Path] = map[string]any{"online": f.Online, "inKbps": f.InKbps, "viewers": f.Viewers, "width": f.Width, "height": f.Height}
		}
	}
	out := []map[string]any{}
	for _, c := range set.Cameras {
		out = append(out, map[string]any{"id": "cam:" + c.ID, "name": c.Name, "source": "camera", "enabled": c.Video,
			"detail": "RTSP pull from the camera · HD + SD, chosen per screen", "status": stats[video.PathHD(c.ID)]})
	}
	// senders the gateway turned away (wrong key, unknown path), per push feed
	var refused map[string]video.Refusal
	if s.Video != nil {
		var push []string
		online := map[string]bool{}
		for _, f := range set.Video.Feeds {
			if f.Source == "push" {
				push = append(push, f.ID)
				online[f.ID], _ = stats[f.ID]["online"].(bool)
			}
		}
		refused = s.Video.Refused(push, online)
	}
	for _, f := range set.Video.Feeds {
		var st map[string]any
		if m, ok := stats[f.ID]; ok {
			st = m
		}
		if rf, ok := refused[f.ID]; ok {
			if on, _ := st["online"].(bool); !on {
				if st == nil {
					st = map[string]any{"online": false}
				}
				st["lastRefused"] = rf
			}
		}
		out = append(out, s.feedView(r, f, st))
	}
	writeJSON(w, 200, map[string]any{"feeds": out, "managed": set.Video.Managed})
}

func (s *Server) saveFeed(w http.ResponseWriter, r *http.Request) {
	var b struct { // key and owner are never taken from the caller
		Name   string `json:"name"`
		Source string `json:"source"`
		URL    string `json:"url"`
	}
	if !decode(w, r, &b) {
		return
	}
	k := feedCaller(r)
	id := r.PathValue("id")
	f := settings.Feed{Name: strings.TrimSpace(b.Name), Source: b.Source, URL: strings.TrimSpace(b.URL)}
	if f.Name == "" || len([]rune(f.Name)) > 40 {
		fail(w, 422, "name is required (40 characters at most)")
		return
	}
	switch f.Source {
	case "push":
		f.URL = ""
	case "pull":
		if !strings.HasPrefix(f.URL, "rtsp://") && !strings.HasPrefix(f.URL, "rtsps://") && !strings.HasPrefix(f.URL, "srt://") {
			fail(w, 422, "the address must start with rtsp:// or srt://")
			return
		}
	default:
		fail(w, 422, "source must be push (sent here) or pull (an address)")
		return
	}
	if id == "" {
		f.ID = slug(f.Name, "feed")
	} else {
		f.ID = id
	}
	if !feedIDRe.MatchString(f.ID) || strings.HasPrefix(f.ID, "cam") {
		fail(w, 422, "pick a name that starts with a letter or number (and not \"cam\")")
		return
	}
	denied := false
	next, err := s.Settings.Update(func(st *settings.Settings) error {
		for i, old := range st.Video.Feeds {
			if old.ID == f.ID {
				if id == "" {
					return fmt.Errorf("a feed named %q exists", old.Name)
				}
				if !canManageFeed(k, old) {
					denied = true
					return errors.New("denied")
				}
				f.Owner = old.Owner
				if f.Source == "push" && old.Source == "push" {
					f.Key = old.Key // senders keep working
				}
				st.Video.Feeds[i] = f
				return nil
			}
		}
		if id != "" {
			return fmt.Errorf("%w: no such feed", errNotFound)
		}
		if k != nil {
			f.Owner = k.Conn
		}
		st.Video.Feeds = append(st.Video.Feeds, f)
		return nil
	})
	if denied {
		fail(w, 403, "this add-on can only change the video feeds it added")
		return
	}
	if s.settingsErr(w, err) {
		return
	}
	saved, _ := next.Video.Feed(f.ID) // with its publish key
	s.Log.Info("video feed saved", "feed", f.ID, "source", f.Source, "client", clientIP(r))
	verb := "changed"
	if id == "" {
		verb = "added"
	}
	s.auditFeed(r, "video feed "+verb+": "+f.ID+" ("+f.Source+")")
	writeJSON(w, 200, s.savedFeed(r, saved))
}

// savedFeed is the answer to a change: the feed with its sender details.
func (s *Server) savedFeed(r *http.Request, f settings.Feed) map[string]any {
	v := map[string]any{"id": f.ID, "name": f.Name, "source": f.Source, "url": f.URL}
	if f.Owner != "" {
		v["owner"] = f.Owner
	}
	if f.Source == "push" {
		v["key"] = f.Key
		v["push"] = pushInfo(s.feedHost(r), f, true)
	}
	return v
}

func (s *Server) newFeedKey(w http.ResponseWriter, r *http.Request) {
	id, k := r.PathValue("id"), feedCaller(r)
	denied := false
	next, err := s.Settings.Update(func(st *settings.Settings) error {
		for i, f := range st.Video.Feeds {
			if f.ID != id {
				continue
			}
			if !canManageFeed(k, f) {
				denied = true
				return errors.New("denied")
			}
			if f.Source != "push" {
				return errors.New("only a feed sent here has a publish key")
			}
			st.Video.Feeds[i].Key = settings.NewFeedKey()
			return nil
		}
		return fmt.Errorf("%w: no such feed", errNotFound)
	})
	if denied {
		fail(w, 403, "this add-on can only change the video feeds it added")
		return
	}
	if s.settingsErr(w, err) {
		return
	}
	f, _ := next.Video.Feed(id)
	s.Log.Info("video feed got a new publish key", "feed", id, "client", clientIP(r))
	s.cutFeedSender(r.Context(), next, id, "publisher disconnected after new key")
	s.auditFeed(r, "video feed "+id+": new publish key")
	writeJSON(w, 200, s.savedFeed(r, f))
}

func (s *Server) deleteFeed(w http.ResponseWriter, r *http.Request) {
	id, k := r.PathValue("id"), feedCaller(r)
	denied := false
	push := false
	next, err := s.Settings.Update(func(st *settings.Settings) error {
		keep := []settings.Feed{}
		found := false
		for _, f := range st.Video.Feeds {
			if f.ID != id {
				keep = append(keep, f)
				continue
			}
			found = true
			push = f.Source == "push"
			if !canManageFeed(k, f) {
				denied = true
				return errors.New("denied")
			}
		}
		if !found {
			return fmt.Errorf("%w: no such feed", errNotFound)
		}
		st.Video.Feeds = keep
		return nil
	})
	if denied {
		fail(w, 403, "this add-on can only remove the video feeds it added")
		return
	}
	if s.settingsErr(w, err) {
		return
	}
	s.Log.Info("video feed removed", "feed", id, "client", clientIP(r))
	if push {
		s.cutFeedSender(r.Context(), next, id, "publisher disconnected after feed removed")
	}
	s.auditFeed(r, "video feed removed: "+id)
	w.WriteHeader(204)
}

// cutFeedSender disconnects whoever is publishing feed id on the managed
// gateway once next's credentials are in effect there (MediaMTX checks a
// key only when a sender connects). The key never goes in the log.
func (s *Server) cutFeedSender(ctx context.Context, next settings.Settings, id, msg string) {
	// Only our own running gateway: a stopped one reads the new config
	// when it starts, and the API port may belong to someone else then.
	if s.Video == nil || !next.Video.Managed || !s.Video.Running() {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	kicked, err := s.Video.CutPublishers(ctx, next, id)
	for _, k := range kicked {
		s.Log.Info(msg, "feed", id, "session", k.Kind)
	}
	if err != nil {
		s.Log.Warn("could not disconnect the feed's current sender", "feed", id, "err", err)
	}
}
