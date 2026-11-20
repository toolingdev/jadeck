package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"ptz/internal/devices"
)

func (s *Server) deviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/devices", s.devicesInfo)
	// Generic routes, plus the Python-era names the current dashboard uses.
	for _, p := range []string{"switcher", "vmix"} {
		mux.HandleFunc("GET /api/"+p+"/status", s.switcherStatus)
		mux.HandleFunc("POST /api/"+p+"/preview", s.switcherPreview)
		mux.HandleFunc("POST /api/"+p+"/cut", s.switcherCut)
		mux.HandleFunc("POST /api/"+p+"/fade", s.switcherFade)
	}
	for _, p := range []string{"presentation", "proclaim"} {
		mux.HandleFunc("GET /api/"+p+"/status", s.presentationStatus)
		mux.HandleFunc("POST /api/"+p+"/{action}", s.presentationDo)
	}
	for _, p := range []string{"mixer", "wing"} {
		mux.HandleFunc("GET /api/"+p+"/status", s.mixerStatus)
		mux.HandleFunc("POST /api/"+p+"/mute", s.mixerMute)
	}
	mux.HandleFunc("POST /api/mixer/channel-mute", s.mixerChannelMute)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) devicesInfo(w http.ResponseWriter, r *http.Request) {
	d := s.Devs()
	kind := func(cfg devices.DeviceConfig, live bool, err error) map[string]any {
		return map[string]any{"driver": cfg.Driver, "label": cfg.Label, "configured": cfg.Driver != "", "ready": live, "error": errText(err)}
	}
	writeJSON(w, 200, map[string]any{
		"drivers":      devices.Drivers(),
		"switcher":     kind(d.SwitcherCfg, d.Switcher != nil, d.SwitcherErr),
		"presentation": kind(d.PresentationCfg, d.Presentation != nil, d.PresentationErr),
		"mixer":        kind(d.MixerCfg, d.Mixer != nil, d.MixerErr),
		"favorites":    d.Favorites,
	})
}

func short(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 5*time.Second)
}

// ---------------------------------------------------------- switcher

func (s *Server) switcherState(ctx context.Context) map[string]any {
	d := s.Devs()
	out := map[string]any{"driver": d.SwitcherCfg.Driver, "label": d.SwitcherCfg.Label,
		"simulated": d.SwitcherCfg.Driver == "sim", "connected": false,
		"inputs": []any{}, "preview": 0, "active": 0, "streaming": false, "recording": false,
		"fadeToBlack": false, "version": "", "edition": ""}
	if d.Switcher == nil {
		out["error"] = "switcher not set up"
		if d.SwitcherErr != nil {
			out["error"] = d.SwitcherErr.Error()
		}
		return out
	}
	st, err := d.Switcher.State(ctx)
	if err != nil {
		out["error"] = err.Error()
		s.track("switcher ("+d.SwitcherCfg.Driver+")", false, err.Error())
		return out
	}
	s.track("switcher ("+d.SwitcherCfg.Driver+")", true, st.Version)
	out["connected"] = true
	out["inputs"], out["preview"], out["active"] = st.Inputs, st.Preview, st.Active
	out["streaming"], out["recording"], out["fadeToBlack"] = st.Streaming, st.Recording, st.FadeToBlack
	out["version"], out["edition"] = st.Version, st.Edition
	return out
}

func (s *Server) switcherStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := short(r)
	defer cancel()
	writeJSON(w, 200, s.switcherState(ctx))
}

func (s *Server) switcherAct(w http.ResponseWriter, r *http.Request, do func(context.Context, devices.Switcher) error) {
	if s.Devs().Switcher == nil {
		fail(w, 503, "switcher not set up")
		return
	}
	ctx, cancel := short(r)
	defer cancel()
	if err := do(ctx, s.Devs().Switcher); err != nil {
		fail(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, s.switcherState(ctx))
}

func (s *Server) switcherPreview(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Input int `json:"input"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Input < 1 || b.Input > 999 {
		fail(w, 422, "input must be 1-999")
		return
	}
	s.switcherAct(w, r, func(ctx context.Context, sw devices.Switcher) error { return sw.Preview(ctx, b.Input) })
}

func (s *Server) switcherCut(w http.ResponseWriter, r *http.Request) {
	s.switcherAct(w, r, func(ctx context.Context, sw devices.Switcher) error { return sw.Cut(ctx) })
}

func (s *Server) switcherFade(w http.ResponseWriter, r *http.Request) {
	b := struct {
		DurationMs int `json:"durationMs"`
	}{1000}
	if r.ContentLength != 0 && !decode(w, r, &b) {
		return
	}
	if b.DurationMs < 0 || b.DurationMs > 10000 {
		fail(w, 422, "durationMs must be 0-10000")
		return
	}
	s.switcherAct(w, r, func(ctx context.Context, sw devices.Switcher) error { return sw.Fade(ctx, b.DurationMs) })
}

// ------------------------------------------------------ presentation

func (s *Server) presentationStatus(w http.ResponseWriter, r *http.Request) {
	d := s.Devs()
	out := map[string]any{"driver": d.PresentationCfg.Driver, "label": d.PresentationCfg.Label,
		"simulated": d.PresentationCfg.Driver == "sim", "available": false, "actions": []string{}}
	if d.Presentation == nil {
		out["port"] = "presentation not set up"
		if d.PresentationErr != nil {
			out["port"] = d.PresentationErr.Error()
		}
		writeJSON(w, 200, out)
		return
	}
	info := d.Presentation.Info()
	s.track("slides ("+d.PresentationCfg.Driver+")", info.Available, info.Target)
	out["available"], out["port"], out["actions"] = info.Available, info.Target, d.Presentation.Actions()
	for k, v := range info.Details { // channel, notes, lastSent for the MIDI driver
		out[k] = v
	}
	writeJSON(w, 200, out)
}

func (s *Server) presentationDo(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action == "status" {
		s.presentationStatus(w, r)
		return
	}
	if s.Devs().Presentation == nil {
		fail(w, 503, "presentation not set up")
		return
	}
	ctx, cancel := short(r)
	defer cancel()
	det, err := s.Devs().Presentation.Do(ctx, action)
	if errors.Is(err, devices.ErrUnknownAction) {
		fail(w, 400, err.Error())
		return
	}
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	out := map[string]any{"ok": true}
	for k, v := range det {
		out[k] = v
	}
	writeJSON(w, 200, out)
}

// ------------------------------------------------------------- mixer

func (s *Server) mixerStatus(w http.ResponseWriter, r *http.Request) {
	d := s.Devs()
	ctx, cancel := short(r)
	defer cancel()
	chans := func(read bool) []map[string]any {
		out := []map[string]any{}
		for _, f := range d.Favorites {
			var muted any
			if read && f.Channel != nil {
				if m, err := d.Mixer.GetMute(ctx, *f.Channel); err == nil {
					muted = m
				}
			}
			out = append(out, map[string]any{"slot": f.Slot, "label": f.Label, "channel": f.Channel, "muted": muted})
		}
		return out
	}
	out := map[string]any{"driver": d.MixerCfg.Driver, "label": d.MixerCfg.Label,
		"simulated": d.MixerCfg.Driver == "sim", "configured": d.Mixer != nil, "console": nil, "connected": false}
	if d.Mixer == nil {
		out["error"] = "console address not set up yet"
		if d.MixerErr != nil {
			out["error"] = d.MixerErr.Error()
		}
		out["channels"] = chans(false)
		writeJSON(w, 200, out)
		return
	}
	id, err := d.Mixer.Probe(ctx)
	s.track("mixer ("+d.MixerCfg.Driver+")", err == nil, errText(err))
	if err != nil {
		out["error"] = err.Error()
		out["channels"] = chans(false)
		writeJSON(w, 200, out)
		return
	}
	out["connected"], out["console"], out["channels"] = true, id, chans(true)
	writeJSON(w, 200, out)
}

func (s *Server) mixerMute(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Slot  int   `json:"slot"`
		Muted *bool `json:"muted"`
	}
	if !decode(w, r, &b) {
		return
	}
	ctx, cancel := short(r)
	defer cancel()
	sl, back, err := s.Devs().MuteSlot(ctx, b.Slot, b.Muted)
	switch {
	case errors.Is(err, devices.ErrUnassigned):
		fail(w, 409, err.Error())
	case errors.Is(err, devices.ErrNotConfigured):
		fail(w, 503, err.Error())
	case err != nil:
		fail(w, 502, err.Error())
	default:
		writeJSON(w, 200, map[string]any{"slot": sl.Slot, "label": sl.Label, "channel": sl.Channel, "muted": back})
	}
}

func (s *Server) mixerChannelMute(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Channel int   `json:"channel"`
		Muted   *bool `json:"muted"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Muted == nil {
		fail(w, 422, "muted (true/false) is required")
		return
	}
	ctx, cancel := short(r)
	defer cancel()
	back, err := s.Devs().MuteChannel(ctx, b.Channel, *b.Muted)
	switch {
	case errors.Is(err, devices.ErrNotConfigured):
		fail(w, 503, err.Error())
	case err != nil:
		fail(w, 502, err.Error())
	default:
		writeJSON(w, 200, map[string]any{"channel": b.Channel, "muted": back})
	}
}
