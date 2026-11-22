// Package cams runs one camera.Service per configured camera and swaps
// them live when cameras are added, edited or removed in Settings.
package cams

import (
	"context"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"

	"ptz/internal/camera"
	"ptz/internal/config"
	"ptz/internal/settings"
	"ptz/internal/store"
)

// Unit is one running camera.
type Unit struct {
	Cfg     settings.Camera
	Svc     *camera.Service
	Presets *store.Store
	cancel  context.CancelFunc
}

type Registry struct {
	DataDir  string
	Log      *slog.Logger
	OnStatus func(id string, st camera.Status)

	ctx    context.Context
	mu     sync.RWMutex
	units  map[string]*Unit
	order  []string
	mainID string
}

func New(ctx context.Context, dataDir string, log *slog.Logger) *Registry {
	return &Registry{ctx: ctx, DataDir: dataDir, Log: log, units: map[string]*Unit{}}
}

// ServiceConfig maps a saved camera to the camera service's config.
func ServiceConfig(c settings.Camera) config.Config {
	return config.Config{CameraHost: c.Host, CameraPort: c.ViscaPort, CameraHTTP: c.HTTPBase(),
		CameraUser: c.User, CameraPass: c.Pass}
}

// LimitsOf maps a camera's controls to the service's operator speeds.
// A camera nobody set speeds for keeps the full range.
func LimitsOf(c settings.Camera) camera.Limits {
	if c.Controls == nil {
		return camera.DefaultLimits
	}
	k := c.Ctl()
	return camera.Limits{Pan: k.PanSpeed, Tilt: k.TiltSpeed, Zoom: k.ZoomSpeed, Focus: k.FocusSpeed - 1, Recall: k.RecallSpeed}
}

func connChanged(a, b settings.Camera) bool {
	return a.Host != b.Host || a.ViscaPort != b.ViscaPort || a.HTTPPort != b.HTTPPort ||
		a.User != b.User || a.Pass != b.Pass || a.Presets != b.Presets
}

// Apply brings the running cameras in line with s.
func (r *Registry) Apply(s settings.Settings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := map[string]settings.Camera{}
	r.order = r.order[:0]
	for _, c := range s.Cameras {
		want[c.ID] = c
		r.order = append(r.order, c.ID)
	}
	if m := s.Main(); m != nil {
		r.mainID = m.ID
	} else {
		r.mainID = ""
	}
	for id, u := range r.units {
		c, keep := want[id]
		if keep && !connChanged(u.Cfg, c) {
			// name / video flags / controls only: same running camera, a
			// new Unit (a Unit is never changed once handed out, so
			// readers holding the old one don't race this write)
			nu := *u
			nu.Cfg = c
			r.units[id] = &nu
			u.Svc.SetLimits(LimitsOf(c))
			continue
		}
		r.stopLocked(u)
		delete(r.units, id)
	}
	for id, c := range want {
		if _, ok := r.units[id]; ok {
			continue
		}
		u, err := r.start(c)
		if err != nil {
			r.Log.Error("camera not started", "camera", c.Name, "err", err)
			continue
		}
		r.units[id] = u
	}
}

func (r *Registry) start(c settings.Camera) (*Unit, error) {
	file := c.Presets
	if file == "" {
		file = "presets-" + c.ID + ".db"
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.DataDir, file)
	}
	ps, err := store.OpenSeeded(path, filepath.Base(file) == "presets.db")
	if err != nil {
		return nil, err
	}
	svc := camera.New(ServiceConfig(c), r.Log.With("camera", c.Name))
	svc.SetLimits(LimitsOf(c))
	id := c.ID
	svc.OnChange = func(st camera.Status) {
		if r.OnStatus != nil {
			r.OnStatus(id, st)
		}
	}
	ctx, cancel := context.WithCancel(r.ctx)
	svc.Start(ctx)
	return &Unit{Cfg: c, Svc: svc, Presets: ps, cancel: cancel}, nil
}

func (r *Registry) stopLocked(u *Unit) {
	u.Svc.ForceStop()
	u.cancel()
	u.Presets.Close()
}

// Get returns the camera with id; "" means the main camera. nil when
// there is no such camera.
func (r *Registry) Get(id string) *Unit {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id == "" {
		id = r.mainID
	}
	return r.units[id]
}

func (r *Registry) Main() *Unit { return r.Get("") }

func (r *Registry) MainID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mainID
}

// List returns the cameras in Settings order.
func (r *Registry) List() []*Unit {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Unit, 0, len(r.units))
	for _, id := range r.order {
		if u := r.units[id]; u != nil {
			out = append(out, u)
		}
	}
	if len(out) != len(r.units) { // defensive: never hide a running camera
		out = out[:0]
		for _, u := range r.units {
			out = append(out, u)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Cfg.Name < out[j].Cfg.Name })
	}
	return out
}

// Busy reports whether any camera is moving or mid-operation.
func (r *Registry) Busy() (driving, op bool) {
	for _, u := range r.List() {
		driving = driving || u.Svc.Driving()
		op = op || u.Svc.OperationActive()
	}
	return
}

// StopAll stops every camera's motion (shutdown).
func (r *Registry) StopAll() {
	for _, u := range r.List() {
		u.Svc.ForceStop()
	}
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, u := range r.units {
		r.stopLocked(u)
		delete(r.units, id)
	}
}
