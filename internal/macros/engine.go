package macros

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Actions is what the engine drives. Each func may be nil when that
// device isn't configured; a step needing it then fails cleanly.
type Actions struct {
	// MuteChannel must set AND verify by read-back.
	MuteChannel     func(ctx context.Context, ch int, muted bool) (bool, error)
	SwitcherPreview func(ctx context.Context, input int) error
	SwitcherCut     func(ctx context.Context) error
	SwitcherFade    func(ctx context.Context, ms int) error
	Slides          func(ctx context.Context, action string) error
	CameraPreset    func(ctx context.Context, name string) error
}

type StepResult struct {
	Step   int    `json:"step"`
	Type   string `json:"type"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
	Ms     int64  `json:"ms"`
}

type Report struct {
	MacroID    int64        `json:"macroId"`
	Name       string       `json:"name"`
	OK         bool         `json:"ok"`
	FailedStep *int         `json:"failedStep"`
	Error      *string      `json:"error"`
	StartedAt  float64      `json:"startedAt"`
	FinishedAt float64      `json:"finishedAt"`
	Steps      []StepResult `json:"steps"`
}

type Engine struct {
	Store       *Store
	Do          Actions
	StepTimeout time.Duration
	RunDeadline time.Duration

	mu      sync.Mutex
	running bool
	current map[string]any
}

func NewEngine(st *Store, do Actions) *Engine {
	return &Engine{Store: st, Do: do, StepTimeout: 10 * time.Second, RunDeadline: 180 * time.Second}
}

func (e *Engine) Status() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	var cur map[string]any
	if e.current != nil {
		cur = map[string]any{}
		for k, v := range e.current {
			cur[k] = v
		}
	}
	return map[string]any{"running": e.running, "current": cur}
}

var errMissing = errors.New("not set up on this install")

// Run executes one macro. A step failure is a report with ok=false,
// not an error; errors are for unknown macros and concurrent runs.
func (e *Engine) Run(ctx context.Context, id int64) (*Report, error) {
	m, err := e.Store.Get(id)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("%w: no macro with id %d", ErrNotFound, id)
	}
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil, fmt.Errorf("%w: another macro is already running — wait for it to finish", ErrConflict)
	}
	e.running = true
	started := now()
	e.current = map[string]any{"macroId": id, "name": m.Name, "step": 0, "totalSteps": len(m.Steps), "startedAt": started}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running, e.current = false, nil
		e.mu.Unlock()
	}()

	runCtx, cancel := context.WithTimeout(ctx, e.RunDeadline)
	defer cancel()
	rep := &Report{MacroID: id, Name: m.Name, StartedAt: started, Steps: []StepResult{}}
	for i, st := range m.Steps {
		e.mu.Lock()
		e.current["step"] = i + 1
		e.mu.Unlock()
		res := StepResult{Step: i + 1, Type: st.Type, Label: Describe(st)}
		t0 := time.Now()
		detail, err := e.step(runCtx, st)
		res.Ms = time.Since(t0).Milliseconds()
		if err != nil {
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				err = fmt.Errorf("the run passed its %v deadline — stopped at this step", e.RunDeadline)
			}
			res.Error = err.Error()
			rep.Steps = append(rep.Steps, res)
			n := i + 1
			msg := fmt.Sprintf("step %d failed: %s", n, res.Error)
			rep.FailedStep, rep.Error = &n, &msg
			break
		}
		res.OK, res.Detail = true, detail
		rep.Steps = append(rep.Steps, res)
	}
	rep.OK = rep.FailedStep == nil
	rep.FinishedAt = now()
	e.Store.RecordRun(id, rep)
	return rep, nil
}

func (e *Engine) step(ctx context.Context, s Step) (string, error) {
	if s.Type == "delay" {
		d := time.Duration(*s.Ms) * time.Millisecond
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < d {
			return "", fmt.Errorf("a %d ms delay would pass the run deadline", *s.Ms)
		}
		select {
		case <-time.After(d):
			return fmt.Sprintf("waited %d ms", *s.Ms), nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	sctx, cancel := context.WithTimeout(ctx, e.StepTimeout)
	defer cancel()
	done := make(chan struct{})
	var detail string
	var err error
	go func() {
		defer close(done)
		detail, err = e.device(sctx, s)
	}()
	select {
	case <-done:
		return detail, err
	case <-sctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("the device did not answer within %v", e.StepTimeout)
	}
}

func (e *Engine) device(ctx context.Context, s Step) (string, error) {
	d := e.Do
	switch s.Type {
	case "mixer_mute":
		if d.MuteChannel == nil {
			return "", fmt.Errorf("mixer %w", errMissing)
		}
		back, err := d.MuteChannel(ctx, s.Channel, *s.Muted)
		if err != nil {
			return "", err
		}
		state := "live"
		if back {
			state = "muted"
		}
		return fmt.Sprintf("channel %d read back %s", s.Channel, state), nil
	case "switcher":
		switch s.Action {
		case "preview":
			if d.SwitcherPreview == nil {
				return "", fmt.Errorf("switcher %w", errMissing)
			}
			return fmt.Sprintf("input %d in preview", s.Input), d.SwitcherPreview(ctx, s.Input)
		case "fade":
			if d.SwitcherFade == nil {
				return "", fmt.Errorf("switcher %w", errMissing)
			}
			return fmt.Sprintf("faded over %d ms", s.DurationMs), d.SwitcherFade(ctx, s.DurationMs)
		default:
			if d.SwitcherCut == nil {
				return "", fmt.Errorf("switcher %w", errMissing)
			}
			return "cut", d.SwitcherCut(ctx)
		}
	case "slides":
		if d.Slides == nil {
			return "", fmt.Errorf("presentation %w", errMissing)
		}
		return "sent " + s.Action, d.Slides(ctx, s.Action)
	case "camera_preset":
		if d.CameraPreset == nil {
			return "", fmt.Errorf("camera %w", errMissing)
		}
		return "recalled " + s.Preset, d.CameraPreset(ctx, s.Preset)
	}
	return "", fmt.Errorf("cannot run step type %q", s.Type)
}
