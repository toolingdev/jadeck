package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"rsc.io/qr"

	"ptz/internal/camera"
	"ptz/internal/cams"
	"ptz/internal/netinfo"
	"ptz/internal/settings"
	"ptz/internal/update"
	"ptz/internal/video"
)

// camFor picks the camera a request is about: X-PTZ-Camera header or
// ?cam=, else the main camera. It answers the error itself.
func (s *Server) camFor(w http.ResponseWriter, r *http.Request) *cams.Unit {
	id := r.Header.Get("X-PTZ-Camera")
	if id == "" {
		id = r.URL.Query().Get("cam")
	}
	u := s.Cams.Get(id)
	if u == nil && id != "" {
		u = s.Cams.Main() // a phone remembering a removed camera falls back to the main one
	}
	if u == nil {
		fail(w, 503, "no camera set up yet — add one in Settings → Cameras")
	}
	return u
}

type camOut struct {
	settings.Camera
	HasPassword bool           `json:"hasPassword"`
	Main        bool           `json:"main"`
	Status      *camera.Status `json:"status"`
}

func (s *Server) cameraList() []camOut {
	set := s.Settings.Get()
	main := set.Main()
	out := []camOut{}
	for _, c := range set.Cameras {
		o := camOut{Camera: c, HasPassword: c.Pass != "", Main: main != nil && main.ID == c.ID}
		o.Pass = ""
		if u := s.Cams.Get(c.ID); u != nil {
			st := u.Svc.Snapshot()
			o.Status = &st
		}
		out = append(out, o)
	}
	return out
}

type camIn struct {
	settings.Camera
	ClearPassword bool `json:"clearPassword"`
}

func (s *Server) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cameras", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.cameraList())
	})
	mux.HandleFunc("POST /api/cameras", func(w http.ResponseWriter, r *http.Request) {
		var b camIn
		if !decode(w, r, &b) {
			return
		}
		c := b.Camera
		c.Defaults()
		if err := c.Validate(); err != nil {
			fail(w, 422, err.Error())
			return
		}
		c.ID = settings.NewID()
		c.Video = true
		_, err := s.Settings.Update(func(st *settings.Settings) error {
			for _, o := range st.Cameras {
				if o.Host == c.Host && o.ViscaPort == c.ViscaPort {
					return fmt.Errorf("%s is already set up as %q", c.Host, o.Name)
				}
			}
			c.Presets = "presets.db" // the first camera keeps the original preset names
			for _, o := range st.Cameras {
				if o.Presets == "presets.db" {
					c.Presets = "presets-" + c.ID + ".db"
				}
			}
			st.Cameras = append(st.Cameras, c)
			if st.MainID == "" || st.Camera(st.MainID) == nil {
				st.MainID = c.ID
			}
			return nil
		})
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		s.Log.Info("camera added", "name", c.Name, "host", c.Host)
		writeJSON(w, 201, s.cameraList())
	})
	mux.HandleFunc("PUT /api/cameras/{id}", func(w http.ResponseWriter, r *http.Request) {
		var b camIn
		if !decode(w, r, &b) {
			return
		}
		id := r.PathValue("id")
		_, err := s.Settings.Update(func(st *settings.Settings) error {
			old := st.Camera(id)
			if old == nil {
				return errNotFound
			}
			c := b.Camera
			c.ID, c.Presets = old.ID, old.Presets
			if c.Pass == "" && !b.ClearPassword {
				c.Pass = old.Pass
			}
			c.Defaults()
			if err := c.Validate(); err != nil {
				return err
			}
			*old = c
			return nil
		})
		if s.settingsErr(w, err) {
			return
		}
		writeJSON(w, 200, s.cameraList())
	})
	mux.HandleFunc("DELETE /api/cameras/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_, err := s.Settings.Update(func(st *settings.Settings) error {
			for i, c := range st.Cameras {
				if c.ID == id {
					st.Cameras = append(st.Cameras[:i], st.Cameras[i+1:]...)
					if st.MainID == id {
						st.MainID = ""
						if len(st.Cameras) > 0 {
							st.MainID = st.Cameras[0].ID
						}
					}
					return nil
				}
			}
			return errNotFound
		})
		if s.settingsErr(w, err) {
			return
		}
		writeJSON(w, 200, s.cameraList())
	})
	mux.HandleFunc("POST /api/cameras/{id}/main", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_, err := s.Settings.Update(func(st *settings.Settings) error {
			if st.Camera(id) == nil {
				return errNotFound
			}
			st.MainID = id
			return nil
		})
		if s.settingsErr(w, err) {
			return
		}
		writeJSON(w, 200, s.cameraList())
	})
	mux.HandleFunc("POST /api/cameras/test", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			settings.Camera
			ID string `json:"id"` // editing a saved camera: blank password = the saved one
		}
		if !decode(w, r, &b) {
			return
		}
		c := b.Camera
		if b.ID != "" && c.Pass == "" {
			set := s.Settings.Get()
			if old := set.Camera(b.ID); old != nil {
				c.Pass = old.Pass
			}
		}
		c.Host = strings.TrimSpace(c.Host)
		if c.Host == "" {
			fail(w, 422, "type the camera's IP address")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		writeJSON(w, 200, cams.Probe(ctx, c))
	})
	mux.HandleFunc("GET /api/cameras/scan", func(w http.ResponseWriter, r *http.Request) {
		cands := cams.LocalCandidates()
		if s.Mock {
			cands = append(cands, cams.Candidate{Host: "127.0.0.1", ViscaPort: 15678, HTTPPort: 18081},
				cams.Candidate{Host: "127.0.0.1", ViscaPort: 15679, HTTPPort: 18082})
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		found := cams.Scan(ctx, cands)
		set := s.Settings.Get()
		for i := range found {
			for _, c := range set.Cameras {
				if c.Host == found[i].Host && c.ViscaPort == found[i].ViscaPort {
					found[i].Known = c.Name
				}
			}
		}
		writeJSON(w, 200, map[string]any{"found": found, "scanned": len(cands)})
	})

	// ---------------------------------------------------------- video
	mux.HandleFunc("GET /api/video", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.videoInfo(r))
	})
	mux.HandleFunc("PUT /api/video", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			settings.Video
			Cameras map[string]bool `json:"cameras"`
		}
		if !decode(w, r, &b) {
			return
		}
		b.External = strings.TrimSpace(b.External)
		_, err := s.Settings.Update(func(st *settings.Settings) error {
			feeds := st.Video.Feeds // edited under Settings → Video feeds
			st.Video = b.Video
			st.Video.Feeds = feeds
			if b.Program || b.Confidence { // older clients' switches
				st.Video.Feeds = nil
				st.Video.MigrateFeeds()
			}
			for i := range st.Cameras {
				if v, ok := b.Cameras[st.Cameras[i].ID]; ok {
					st.Cameras[i].Video = v
				}
			}
			return nil
		})
		if s.settingsErr(w, err) {
			return
		}
		writeJSON(w, 200, s.videoInfo(r))
	})
	mux.HandleFunc("POST /api/video/install", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.Video.Install(ctx); err != nil {
			fail(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, s.videoInfo(r))
	})

	// --------------------------------------------------------- system
	mux.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.systemInfo())
	})
	mux.HandleFunc("PUT /api/system", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			settings.System
			ClearToken bool `json:"clearToken"`
		}
		if !decode(w, r, &b) {
			return
		}
		cur := s.Settings.Get().System
		in := b.System
		if s.PortLocked {
			in.Port = cur.Port
		}
		if in.UpdateToken == "" && !b.ClearToken {
			in.UpdateToken = cur.UpdateToken
		}
		if err := in.Validate(); err != nil {
			fail(w, 422, err.Error())
			return
		}
		if in.Port != cur.Port {
			l, err := net.Listen("tcp", net.JoinHostPort(s.BindHost, fmt.Sprint(in.Port)))
			if err != nil {
				fail(w, 409, fmt.Sprintf("port %d is already used by something else on this PC", in.Port))
				return
			}
			l.Close()
		}
		_, err := s.Settings.Update(func(st *settings.Settings) error {
			st.System = in
			return nil
		})
		if s.settingsErr(w, err) {
			return
		}
		writeJSON(w, 200, s.systemInfo())
	})
	mux.HandleFunc("GET /api/system/qr", func(w http.ResponseWriter, r *http.Request) {
		text := r.URL.Query().Get("text")
		if text == "" || len(text) > 300 {
			fail(w, 422, "text must be 1-300 characters")
			return
		}
		c, err := qr.Encode(text, qr.M)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		c.Scale = 8
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(c.PNG())
	})

	// ---------------------------------------------------------- setup
	mux.HandleFunc("GET /api/setup", func(w http.ResponseWriter, r *http.Request) {
		set := s.Settings.Get()
		writeJSON(w, 200, map[string]any{"done": set.SetupDone, "cameras": len(set.Cameras)})
	})
	mux.HandleFunc("POST /api/setup", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Done bool `json:"done"`
		}
		if !decode(w, r, &b) {
			return
		}
		set, err := s.Settings.Update(func(st *settings.Settings) error {
			st.SetupDone = b.Done
			return nil
		})
		if s.settingsErr(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"done": set.SetupDone, "cameras": len(set.Cameras)})
	})
}

var errNotFound = errors.New("camera not found")

func (s *Server) settingsErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errNotFound):
		fail(w, 404, err.Error())
	default:
		fail(w, 422, err.Error())
	}
	return true
}

// lanIP is the address other machines use to reach this PC.
func lanIP() string {
	for _, a := range netinfo.Addrs() {
		if a.Kind == "lan" {
			return a.IP
		}
	}
	return "127.0.0.1"
}

func (s *Server) videoInfo(r *http.Request) map[string]any {
	set := s.Settings.Get()
	ip := lanIP()
	srt := func(path string) string {
		return fmt.Sprintf("srt://%s:%d?streamid=publish:%s", ip, video.PortSRT, path)
	}
	camsOut := []map[string]any{}
	for _, c := range set.Cameras {
		camsOut = append(camsOut, map[string]any{"id": c.ID, "name": c.Name, "video": c.Video,
			"hd": video.PathHD(c.ID), "sd": video.PathSD(c.ID)})
	}
	return map[string]any{"settings": set.Video, "status": s.Video.Status(), "cameras": camsOut,
		"programSrt": srt("program"), "confidenceSrt": srt("confidence")}
}

func (s *Server) systemInfo() map[string]any {
	sys := s.Settings.Get().System
	hasToken := sys.UpdateToken != ""
	sys.UpdateToken = ""
	name, mdnsErr := "", ""
	if s.Names != nil {
		name, mdnsErr = s.Names.State()
	}
	urls := []string{}
	if name != "" && mdnsErr == "" {
		urls = append(urls, portURL(name+".local", sys.Port))
	}
	for _, a := range netinfo.Addrs() {
		urls = append(urls, portURL(a.IP, sys.Port))
	}
	return map[string]any{"system": sys, "hasToken": hasToken, "portLocked": s.PortLocked,
		"addresses": netinfo.Addrs(), "urls": urls, "mdnsError": mdnsErr, "version": s.Version,
		"update": s.Updates.Status()}
}

func portURL(host string, port int) string {
	if port == 80 {
		return "http://" + host
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// streamURLs is /api/config for one camera.
func (s *Server) streamURLs(r *http.Request, u *cams.Unit) map[string]any {
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if host == "" {
		host = "localhost"
	}
	set := s.Settings.Get()
	if !set.Video.Managed && set.Video.External != "" {
		host = set.Video.External
	}
	if strings.Contains(host, ":") { // bare IPv6
		host = "[" + host + "]"
	}
	url := func(port int, path string) string { return fmt.Sprintf("http://%s:%d/%s", host, port, path) }
	out := map[string]any{"snapshot": "/api/snapshot", "camera": ""}
	if u != nil {
		out["camera"] = u.Cfg.ID
		out["snapshot"] = "/api/snapshot?cam=" + u.Cfg.ID
		hd, sd := video.PathHD(u.Cfg.ID), video.PathSD(u.Cfg.ID)
		if !set.Video.Managed && u.Cfg.ID == set.MainID {
			hd, sd = "cam", "cam_sd" // an existing hand-run MediaMTX (stream/mediamtx.yml)
		}
		live := set.Video.External != ""
		if set.Video.Managed && s.Video != nil {
			st := s.Video.Status().State
			live = st == "running" || st == "starting"
		}
		if u.Cfg.Video && live {
			out["whep"], out["hls"] = url(video.PortWebRTC, hd+"/whep"), url(video.PortHLS, hd+"/index.m3u8")
			out["whepSd"], out["hlsSd"] = url(video.PortWebRTC, sd+"/whep"), url(video.PortHLS, sd+"/index.m3u8")
		}
	}
	external := !set.Video.Managed && set.Video.External != ""
	running := external || !set.Video.Managed || s.Video == nil || s.Video.Status().State == "running"
	feeds := []map[string]string{}
	for _, f := range set.Video.Feeds {
		fd := map[string]string{"id": f.ID, "name": f.Name, "source": f.Source}
		if running {
			fd["whep"], fd["hls"] = url(video.PortWebRTC, f.ID+"/whep"), url(video.PortHLS, f.ID+"/index.m3u8")
		}
		feeds = append(feeds, fd)
	}
	out["feeds"] = feeds
	// The Program / Confidence widgets read these (feeds "program" / "confidence").
	if _, ok := set.Video.Feed("program"); (ok && running) || external {
		out["programWhep"], out["programHls"] = url(video.PortWebRTC, "program/whep"), url(video.PortHLS, "program/index.m3u8")
	}
	if _, ok := set.Video.Feed("confidence"); (ok && running) || external {
		out["confidenceWhep"], out["confidenceHls"] = url(video.PortWebRTC, "confidence/whep"), url(video.PortHLS, "confidence/index.m3u8")
	}
	return out
}

// UpdateSource is where releases come from for these settings.
func UpdateSource(sys settings.System, api string) *update.Source {
	return &update.Source{Repo: sys.UpdateRepo, Token: sys.UpdateToken, API: api, Beta: sys.Channel == "beta"}
}

// ApplySettings puts saved settings into effect live (everything but
// the port, which the caller owns).
func (s *Server) ApplySettings(next settings.Settings) {
	s.Cams.Apply(next)
	if s.Video != nil {
		if err := s.Video.Apply(next); err != nil {
			s.Log.Error("stream gateway config not written", "err", err)
		}
	}
	if s.Trust != nil {
		if err := s.Trust.Set(next.System.TrustLAN, strings.Join(next.System.TrustedCIDRs, ",")); err != nil {
			s.Log.Error("trusted networks not applied", "err", err)
		}
	}
	if s.Updates != nil {
		api := ""
		if src := s.Updates.Source; src != nil {
			api = src.API
		}
		s.Updates.SetSource(UpdateSource(next.System, api))
	}
	if s.Names != nil {
		s.Names.Set(next.System.Name)
	}
}
