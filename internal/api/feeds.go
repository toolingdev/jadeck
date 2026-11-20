package api

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

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
}

var feedIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (s *Server) listFeeds(w http.ResponseWriter, r *http.Request) {
	set := s.Settings.Get()
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if !set.Video.Managed && set.Video.External != "" {
		host = set.Video.External
	}
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
	for _, f := range set.Video.Feeds {
		v := map[string]any{"id": f.ID, "name": f.Name, "source": f.Source, "url": f.URL, "enabled": true, "status": stats[f.ID]}
		if f.Source == "push" {
			v["push"] = map[string]any{
				"url":      fmt.Sprintf("srt://%s:%d?streamid=publish:%s", host, video.PortSRT, f.ID),
				"host":     host,
				"port":     video.PortSRT,
				"streamId": "publish:" + f.ID,
			}
		}
		out = append(out, v)
	}
	writeJSON(w, 200, map[string]any{"feeds": out, "managed": set.Video.Managed})
}

func (s *Server) saveFeed(w http.ResponseWriter, r *http.Request) {
	var b settings.Feed
	if !decode(w, r, &b) {
		return
	}
	id := r.PathValue("id")
	b.Name = strings.TrimSpace(b.Name)
	b.URL = strings.TrimSpace(b.URL)
	if b.Name == "" || len([]rune(b.Name)) > 40 {
		fail(w, 422, "name is required (40 characters at most)")
		return
	}
	switch b.Source {
	case "push":
		b.URL = ""
	case "pull":
		if !strings.HasPrefix(b.URL, "rtsp://") && !strings.HasPrefix(b.URL, "rtsps://") && !strings.HasPrefix(b.URL, "srt://") {
			fail(w, 422, "the address must start with rtsp:// or srt://")
			return
		}
	default:
		fail(w, 422, "source must be push (sent here) or pull (an address)")
		return
	}
	if id == "" {
		b.ID = slug(b.Name, "feed")
	} else {
		b.ID = id
	}
	if !feedIDRe.MatchString(b.ID) || strings.HasPrefix(b.ID, "cam") {
		fail(w, 422, "pick a name that starts with a letter or number (and not \"cam\")")
		return
	}
	_, err := s.Settings.Update(func(st *settings.Settings) error {
		for i, f := range st.Video.Feeds {
			if f.ID == b.ID {
				if id == "" {
					return fmt.Errorf("a feed named %q exists", f.Name)
				}
				st.Video.Feeds[i] = b
				return nil
			}
		}
		if id != "" {
			return fmt.Errorf("%w: no such feed", errNotFound)
		}
		st.Video.Feeds = append(st.Video.Feeds, b)
		return nil
	})
	if s.settingsErr(w, err) {
		return
	}
	s.Log.Info("video feed saved", "feed", b.ID, "source", b.Source, "client", clientIP(r))
	writeJSON(w, 200, b)
}

func (s *Server) deleteFeed(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.Settings.Update(func(st *settings.Settings) error {
		var keep []settings.Feed
		for _, f := range st.Video.Feeds {
			if f.ID != id {
				keep = append(keep, f)
			}
		}
		if len(keep) == len(st.Video.Feeds) {
			return fmt.Errorf("%w: no such feed", errNotFound)
		}
		st.Video.Feeds = append([]settings.Feed{}, keep...)
		return nil
	})
	if s.settingsErr(w, err) {
		return
	}
	s.Log.Info("video feed removed", "feed", id, "client", clientIP(r))
	w.WriteHeader(204)
}
