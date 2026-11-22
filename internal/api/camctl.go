package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"ptz/internal/cams"
	"ptz/internal/imageconf"
	"ptz/internal/settings"
)

// Per-camera operator controls (PTZOptics): speeds, Motion Sync, focus
// lock, the on-screen menu, picture scenes and a preset's own recall
// speed and picture. Every value is kept with the camera (settings.json),
// so every phone and iPad sees the same ones.

// builtinScenes are starting points for common rooms. Only the fields the
// camera reports are written, so a model without one just skips it.
var builtinScenes = []settings.Scene{
	{ID: "builtin:lowlight", Name: "Low light", Settings: map[string]any{
		"expMode": "auto", "gainLimit": 13, "backlight": false, "nr2d": "auto", "nr3d": "4", "brightness": 8}},
	{ID: "builtin:sportsday", Name: "Sports day", Settings: map[string]any{
		"expMode": "shutter", "shutter": "1/250", "wbMode": "outdoor", "nr3d": "1", "sharpness": 8}},
	{ID: "builtin:sportsnight", Name: "Sports night", Settings: map[string]any{
		"expMode": "shutter", "shutter": "1/125", "gainLimit": 13, "wbMode": "auto", "nr3d": "3"}},
}

// sceneFields are the picture settings a scene saves: everything but
// the mounting (flip / mirror) and one-off white-balance gains.
var sceneFields = []string{"brightness", "saturation", "contrast", "sharpness", "hue",
	"wbMode", "colorTemp", "redTuning", "blueTuning", "awbSensitivity",
	"expMode", "shutter", "iris", "gain", "brightLevel", "expCompOn", "expComp", "backlight", "gainLimit", "antiFlicker", "drc",
	"nr2d", "nr3d"}

type controlsOut struct {
	settings.Controls
	Scenes  []settings.Scene `json:"scenes"` // always a list (the saved one may be empty)
	Camera  string           `json:"camera"`
	Builtin []settings.Scene `json:"builtinScenes"`
}

func (s *Server) camCtlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/camera/controls", s.getControls)
	mux.HandleFunc("PATCH /api/camera/controls", s.patchControls)
	mux.HandleFunc("POST /api/camera/focus-lock", s.focusLock)
	mux.HandleFunc("POST /api/camera/osd", s.osd)
	mux.HandleFunc("POST /api/camera/scenes", s.saveScene)
	mux.HandleFunc("POST /api/camera/scenes/{id}/apply", s.applyScene)
	mux.HandleFunc("DELETE /api/camera/scenes/{id}", s.deleteScene)
}

func (s *Server) controlsOf(u *cams.Unit) controlsOut {
	c := u.Cfg
	if st := s.Settings.Get(); st.Camera(u.Cfg.ID) != nil {
		c = *st.Camera(u.Cfg.ID)
	}
	k := c.Ctl()
	if k.Scenes == nil {
		k.Scenes = []settings.Scene{}
	}
	return controlsOut{Controls: k, Scenes: k.Scenes, Camera: u.Cfg.ID, Builtin: builtinScenes}
}

// updateControls edits one camera's controls in the saved settings.
func (s *Server) updateControls(u *cams.Unit, fn func(*settings.Controls) error) error {
	_, err := s.Settings.Update(func(st *settings.Settings) error {
		c := st.Camera(u.Cfg.ID)
		if c == nil {
			return errNotFound
		}
		k := c.Ctl() // a copy: the old settings keep theirs
		if err := fn(&k); err != nil {
			return err
		}
		k.Normalize()
		c.Controls = &k
		return nil
	})
	return err
}

func (s *Server) getControls(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	writeJSON(w, 200, s.controlsOf(u))
}

func (s *Server) patchControls(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	var b struct {
		PanSpeed, TiltSpeed, ZoomSpeed, FocusSpeed, RecallSpeed *int
		MotionSync                                              *bool
	}
	if !decode(w, r, &b) {
		return
	}
	if b.RecallSpeed != nil && (*b.RecallSpeed < 0 || *b.RecallSpeed > 24) {
		fail(w, 400, "recall speed is 0 (camera's own) or 1-24")
		return
	}
	for _, v := range []*int{b.PanSpeed, b.TiltSpeed, b.ZoomSpeed} {
		if v != nil && (*v < 1 || *v > 24) {
			fail(w, 400, "speeds are 1-24")
			return
		}
	}
	if b.TiltSpeed != nil && *b.TiltSpeed > 20 || b.ZoomSpeed != nil && *b.ZoomSpeed > 7 || b.FocusSpeed != nil && (*b.FocusSpeed < 0 || *b.FocusSpeed > 8) {
		fail(w, 400, "tilt is 1-20, zoom 1-7, focus 0 (standard) or 1-8")
		return
	}
	if b.MotionSync != nil {
		if err := u.Svc.MotionSync(r.Context(), *b.MotionSync); err != nil {
			fail(w, 502, err.Error())
			return
		}
	}
	err := s.updateControls(u, func(k *settings.Controls) error {
		set := func(dst *int, v *int) {
			if v != nil {
				*dst = *v
			}
		}
		set(&k.PanSpeed, b.PanSpeed)
		set(&k.TiltSpeed, b.TiltSpeed)
		set(&k.ZoomSpeed, b.ZoomSpeed)
		set(&k.FocusSpeed, b.FocusSpeed)
		set(&k.RecallSpeed, b.RecallSpeed)
		if b.MotionSync != nil {
			v := *b.MotionSync
			k.MotionSync = &v
		}
		return nil
	})
	if s.settingsErr(w, err) {
		return
	}
	writeJSON(w, 200, s.controlsOf(u))
}

func (s *Server) focusLock(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	var b struct {
		Locked bool `json:"locked"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !u.Svc.Connected() {
		fail(w, 503, "camera not connected")
		return
	}
	if err := u.Svc.FocusLock(b.Locked); err != nil {
		fail(w, 502, err.Error())
		return
	}
	if err := s.updateControls(u, func(k *settings.Controls) error { k.FocusLocked = b.Locked; return nil }); s.settingsErr(w, err) {
		return
	}
	writeJSON(w, 200, s.controlsOf(u))
}

func (s *Server) osd(w http.ResponseWriter, r *http.Request) {
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
	if !s.motionGuard(w, u) {
		return
	}
	if err := u.Svc.OSD(r.Context(), b.Action); err != nil {
		code := 502
		if strings.HasPrefix(err.Error(), "unknown") {
			code = 400
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// currentPicture reads the camera's picture settings, keeping the scene fields.
func (s *Server) currentPicture(ctx context.Context, u *cams.Unit) (map[string]any, error) {
	c, err := s.fetchConf(ctx, u)
	if err != nil {
		return nil, err
	}
	all := imageconf.ToTyped(c)
	out := map[string]any{}
	for _, f := range sceneFields {
		if v, ok := all[f]; ok {
			out[f] = v
		}
	}
	return out, nil
}

// applyPicture writes picture settings, skipping fields this camera
// doesn't report (a scene saved on another model, or a built-in).
func (s *Server) applyPicture(ctx context.Context, u *cams.Unit, pic map[string]any) error {
	c, err := s.fetchConf(ctx, u)
	if err != nil {
		return errors.New("camera image config unavailable")
	}
	upd := map[string]any{}
	for name, v := range pic {
		if key, ok := imageconf.KeyOf(name); ok {
			if _, has := c.Get(key); has && v != nil {
				upd[name] = v
			}
		}
	}
	if len(upd) == 0 {
		return nil
	}
	n, err := imageconf.Apply(c, upd)
	if err != nil {
		return err
	}
	_, err = u.Svc.HTTPGet(ctx, "/cgi-bin/param.cgi?"+n.PostQuery())
	return err
}

func newSceneID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "s" + hex.EncodeToString(b)
}

func (s *Server) saveScene(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	var b struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &b) {
		return
	}
	name := strings.TrimSpace(b.Name)
	if name == "" || len(name) > 40 {
		fail(w, 422, "give the scene a name (up to 40 characters)")
		return
	}
	pic, err := s.currentPicture(r.Context(), u)
	if err != nil {
		fail(w, 502, "camera image config unavailable")
		return
	}
	err = s.updateControls(u, func(k *settings.Controls) error {
		for i, sc := range k.Scenes {
			if strings.EqualFold(sc.Name, name) { // same name: replace it
				k.Scenes[i].Settings = pic
				k.ActiveScene = sc.ID
				return nil
			}
		}
		if len(k.Scenes) >= 24 {
			return errors.New("24 scenes at most; delete one first")
		}
		sc := settings.Scene{ID: newSceneID(), Name: name, Settings: pic}
		k.Scenes = append(k.Scenes, sc)
		k.ActiveScene = sc.ID
		return nil
	})
	if s.settingsErr(w, err) {
		return
	}
	writeJSON(w, 201, s.controlsOf(u))
}

func (s *Server) findScene(u *cams.Unit, id string) *settings.Scene {
	for _, sc := range builtinScenes {
		if sc.ID == id {
			return &sc
		}
	}
	for _, sc := range s.controlsOf(u).Scenes {
		if sc.ID == id {
			return &sc
		}
	}
	return nil
}

func (s *Server) applyScene(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	sc := s.findScene(u, r.PathValue("id"))
	if sc == nil {
		fail(w, 404, "scene not found")
		return
	}
	if err := s.applyPicture(r.Context(), u, sc.Settings); err != nil {
		fail(w, 502, err.Error())
		return
	}
	if err := s.updateControls(u, func(k *settings.Controls) error { k.ActiveScene = sc.ID; return nil }); s.settingsErr(w, err) {
		return
	}
	writeJSON(w, 200, s.controlsOf(u))
}

func (s *Server) deleteScene(w http.ResponseWriter, r *http.Request) {
	u := s.camFor(w, r)
	if u == nil {
		return
	}
	id := r.PathValue("id")
	err := s.updateControls(u, func(k *settings.Controls) error {
		for i, sc := range k.Scenes {
			if sc.ID == id {
				k.Scenes = append(k.Scenes[:i], k.Scenes[i+1:]...)
				if k.ActiveScene == id {
					k.ActiveScene = ""
				}
				return nil
			}
		}
		return errNotFound
	})
	if s.settingsErr(w, err) {
		return
	}
	writeJSON(w, 200, s.controlsOf(u))
}

// presetSpeed is a preset's own recall speed (0: the camera's).
func (s *Server) presetSpeed(u *cams.Unit, id int64) int {
	return s.controlsOf(u).PresetSpeeds[strconv.FormatInt(id, 10)]
}
