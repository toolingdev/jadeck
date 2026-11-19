package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ptz/internal/devices"
)

// Device setup from the Settings screen: pick a driver per kind, fill its
// form, test it, save. Saving rewrites devices.json (the app owns that
// file) and swaps the live devices without a restart.

type setupBody struct {
	Switcher     map[string]any     `json:"switcher"`
	Presentation map[string]any     `json:"presentation"`
	Mixer        map[string]any     `json:"mixer"`
	Favorites    []devices.Favorite `json:"favorites"`
}

func (s *Server) devConfigRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/devices/setup", s.getSetup)
	mux.HandleFunc("PUT /api/devices/setup", s.putSetup)
	mux.HandleFunc("POST /api/devices/test", s.testDevice)
	mux.HandleFunc("GET /api/devices/options/{name}", func(w http.ResponseWriter, r *http.Request) {
		o := devices.Options(r.PathValue("name"))
		if o == nil {
			o = []devices.Option{}
		}
		writeJSON(w, 200, o)
	})
}

func (s *Server) getSetup(w http.ResponseWriter, r *http.Request) {
	s.devMu.RLock()
	f := s.DevFile
	s.devMu.RUnlock()
	if f == nil {
		f = &devices.File{}
	}
	writeJSON(w, 200, map[string]any{"metas": devices.Metas(), "config": f, "path": s.DevicesPath})
}

func toDeviceConfig(m map[string]any) (*devices.DeviceConfig, error) {
	if m == nil {
		return nil, nil
	}
	if d, _ := m["driver"].(string); d == "" {
		return nil, nil // "None"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var dc devices.DeviceConfig
	if err := json.Unmarshal(b, &dc); err != nil {
		return nil, err
	}
	return &dc, nil
}

func (s *Server) putSetup(w http.ResponseWriter, r *http.Request) {
	var b setupBody
	if !decode(w, r, &b) {
		return
	}
	f := &devices.File{Favorites: b.Favorites}
	var err error
	if f.Switcher, err = toDeviceConfig(b.Switcher); err != nil {
		fail(w, 400, "switcher: "+err.Error())
		return
	}
	if f.Presentation, err = toDeviceConfig(b.Presentation); err != nil {
		fail(w, 400, "slides: "+err.Error())
		return
	}
	if f.Mixer, err = toDeviceConfig(b.Mixer); err != nil {
		fail(w, 400, "mixer: "+err.Error())
		return
	}
	for i, fav := range f.Favorites {
		if fav.Channel < 0 || fav.Channel > 128 {
			fail(w, 400, fmt.Sprintf("favorite %d: channel %d is out of range", i+1, fav.Channel))
			return
		}
	}
	// Close the live MIDI port first so the new set can open it.
	old := s.Devs()
	if old != nil {
		if c, ok := old.Presentation.(devices.Closer); ok {
			c.Close()
		}
	}
	set := devices.Open(f)
	errs := map[string]string{}
	for k, e := range map[string]error{"switcher": set.SwitcherErr, "presentation": set.PresentationErr, "mixer": set.MixerErr} {
		if e != nil {
			errs[k] = e.Error()
		}
	}
	if len(errs) > 0 {
		set.Close()
		writeJSON(w, 400, map[string]any{"detail": "some settings need fixing — nothing was saved", "errors": errs})
		return
	}
	if s.DevicesPath != "" {
		if err := writeConfig(s.DevicesPath, f); err != nil {
			set.Close()
			fail(w, 500, "could not save: "+err.Error())
			return
		}
	}
	s.SetDevices(f, set)
	s.Log.Info("devices updated from Settings", "path", s.DevicesPath)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func writeConfig(path string, f *devices.File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// testDevice opens a device from the form (or reuses the live one if the
// settings are unchanged, so a MIDI port isn't opened twice) and checks
// that it answers.
func (s *Server) testDevice(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Kind   string         `json:"kind"`
		Config map[string]any `json:"config"`
	}
	if !decode(w, r, &b) {
		return
	}
	dc, err := toDeviceConfig(b.Config)
	if err != nil || dc == nil {
		fail(w, 400, "pick a driver first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	live := s.Devs()
	same := func(cur devices.DeviceConfig) bool {
		a, _ := json.Marshal(cur)
		c, _ := json.Marshal(dc)
		return bytes.Equal(canon(a), canon(c))
	}
	var f devices.File
	switch b.Kind {
	case "switcher":
		f.Switcher = dc
	case "presentation":
		f.Presentation = dc
	case "mixer":
		f.Mixer = dc
	default:
		fail(w, 400, "unknown device kind")
		return
	}
	set := live
	reuse := (b.Kind == "switcher" && same(live.SwitcherCfg) && live.Switcher != nil) ||
		(b.Kind == "presentation" && same(live.PresentationCfg) && live.Presentation != nil) ||
		(b.Kind == "mixer" && same(live.MixerCfg) && live.Mixer != nil)
	if !reuse {
		set = devices.Open(&f)
		defer set.Close()
	}
	res := map[string]any{"ok": false}
	switch b.Kind {
	case "switcher":
		if set.SwitcherErr != nil && !reuse {
			res["error"] = set.SwitcherErr.Error()
			break
		}
		st, err := set.Switcher.State(ctx)
		if err != nil {
			res["error"] = err.Error()
			break
		}
		res["ok"] = true
		res["detail"] = fmt.Sprintf("Connected — %d inputs, program %d, preview %d", len(st.Inputs), st.Active, st.Preview)
	case "presentation":
		if set.PresentationErr != nil && !reuse {
			res["error"] = set.PresentationErr.Error()
			break
		}
		info := set.Presentation.Info()
		if !info.Available {
			res["error"] = info.Target
			break
		}
		res["ok"] = true
		res["detail"] = "Ready — sends to " + info.Target
	case "mixer":
		if set.MixerErr != nil && !reuse {
			res["error"] = set.MixerErr.Error()
			break
		}
		id, err := set.Mixer.Probe(ctx)
		if err != nil {
			res["error"] = err.Error()
			break
		}
		res["ok"] = true
		res["detail"] = "Console answered — " + id
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && res["ok"] == false && res["error"] == nil {
		res["error"] = "no answer within 6 s"
	}
	writeJSON(w, 200, res)
}

func canon(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, _ := json.Marshal(v)
	return out
}
