// Package schedule runs actions at a set time: "in 5 minutes cut to
// camera 2". A job is a list of macro steps (or a saved macro by name),
// run by the macro engine when its time comes. Jobs are saved, so they
// survive a restart; one whose time passed while the controller was off
// is marked missed, never run late.
package schedule

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"ptz/internal/macros"
)

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusMissed    = "missed"

	// MaxAhead is how far ahead a job may be scheduled.
	MaxAhead = 24 * time.Hour
	// Grace: a job up to this late (controller busy, clock tick) still runs.
	Grace    = 60 * time.Second
	keepDone = 50
)

type Job struct {
	ID      string         `json:"id"`
	At      time.Time      `json:"at"`
	Label   string         `json:"label"`
	By      string         `json:"by"` // who asked: "Assistant (booth iPad)", "key Claude Code", "Booth"
	Macro   string         `json:"macro,omitempty"`
	Steps   []macros.Step  `json:"steps,omitempty"`
	Status  string         `json:"status"`
	Created time.Time      `json:"created"`
	Ended   *time.Time     `json:"ended,omitempty"`
	Result  string         `json:"result,omitempty"`
	Report  *macros.Report `json:"report,omitempty"`
}

// Runner runs a job's steps (the macro engine).
type Runner func(ctx context.Context, j Job) (*macros.Report, error)

type Store struct {
	path string
	run  Runner
	log  *slog.Logger

	mu     sync.Mutex
	jobs   []Job
	wake   chan struct{}
	notify func()
}

var (
	ErrNotFound = errors.New("no such scheduled action")
	ErrInvalid  = errors.New("invalid scheduled action")
)

// Open loads saved jobs; pending ones already past their time become missed.
func Open(path string, run Runner, log *slog.Logger) (*Store, error) {
	s := &Store{path: path, run: run, log: log, wake: make(chan struct{}, 1)}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.jobs); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	now := time.Now()
	for i := range s.jobs {
		j := &s.jobs[i]
		if (j.Status == StatusPending || j.Status == StatusRunning) && now.Sub(j.At) > Grace {
			j.Status, j.Result = StatusMissed, "the controller wasn't running at that time"
			t := now
			j.Ended = &t
			log.Warn("scheduled action missed", "label", j.Label, "at", j.At.Format(time.Kitchen))
		}
	}
	return s, s.saveLocked()
}

// OnChange is called after any change (live updates to screens).
func (s *Store) OnChange(f func()) { s.notify = f }

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	// keep every pending job and the most recent finished ones
	var pending, finished []Job
	for _, j := range s.jobs {
		if j.Status == StatusPending || j.Status == StatusRunning {
			pending = append(pending, j)
		} else {
			finished = append(finished, j)
		}
	}
	sort.Slice(finished, func(a, b int) bool { return finished[a].At.After(finished[b].At) })
	if len(finished) > keepDone {
		finished = finished[:keepDone]
	}
	s.jobs = append(pending, finished...)
	os.MkdirAll(filepath.Dir(s.path), 0o755)
	b, _ := json.MarshalIndent(s.jobs, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) changed() {
	s.saveLocked()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if s.notify != nil {
		go s.notify()
	}
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Add schedules a job. At must be within MaxAhead.
func (s *Store) Add(j Job) (Job, error) {
	now := time.Now()
	if j.At.Before(now.Add(-time.Second)) {
		return j, fmt.Errorf("%w: that time has passed", ErrInvalid)
	}
	if j.At.After(now.Add(MaxAhead)) {
		return j, fmt.Errorf("%w: at most %v ahead", ErrInvalid, MaxAhead)
	}
	if len(j.Steps) == 0 && j.Macro == "" {
		return j, fmt.Errorf("%w: nothing to do (give steps or a macro)", ErrInvalid)
	}
	if j.Label == "" {
		j.Label = j.Macro
	}
	j.ID, j.Status, j.Created = newID(), StatusPending, now
	s.mu.Lock()
	n := 0
	for _, x := range s.jobs {
		if x.Status == StatusPending {
			n++
		}
	}
	if n >= 100 {
		s.mu.Unlock()
		return j, fmt.Errorf("%w: 100 actions are already waiting", ErrInvalid)
	}
	s.jobs = append(s.jobs, j)
	s.changed()
	s.mu.Unlock()
	s.log.Info("action scheduled", "label", j.Label, "at", j.At.Format(time.Kitchen), "by", j.By)
	return j, nil
}

// Cancel stops a pending job.
func (s *Store) Cancel(id, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			if s.jobs[i].Status != StatusPending {
				return fmt.Errorf("%w: it is already %s", ErrInvalid, s.jobs[i].Status)
			}
			t := time.Now()
			s.jobs[i].Status, s.jobs[i].Ended, s.jobs[i].Result = StatusCancelled, &t, "cancelled by "+by
			s.changed()
			s.log.Info("scheduled action cancelled", "label", s.jobs[i].Label, "by", by)
			return nil
		}
	}
	return ErrNotFound
}

// RunNow moves a pending job's time to now.
func (s *Store) RunNow(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id && s.jobs[i].Status == StatusPending {
			s.jobs[i].At = time.Now()
			s.changed()
			return nil
		}
	}
	return ErrNotFound
}

// List returns pending jobs (soonest first), then recent finished ones.
func (s *Store) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Job(nil), s.jobs...)
	sort.SliceStable(out, func(a, b int) bool {
		pa, pb := out[a].Status == StatusPending, out[b].Status == StatusPending
		if pa != pb {
			return pa
		}
		if pa {
			return out[a].At.Before(out[b].At)
		}
		return out[a].At.After(out[b].At)
	})
	return out
}

// Run is the scheduler loop.
func (s *Store) Run(ctx context.Context) {
	for {
		s.mu.Lock()
		var next *Job
		for i := range s.jobs {
			j := &s.jobs[i]
			if j.Status == StatusPending && (next == nil || j.At.Before(next.At)) {
				next = j
			}
		}
		wait := time.Hour
		var due Job
		if next != nil {
			wait = time.Until(next.At)
			if wait <= 0 {
				next.Status = StatusRunning
				due = *next
				s.changed()
			}
		}
		s.mu.Unlock()
		if due.ID != "" {
			s.execute(ctx, due)
			continue
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.wake:
		case <-t.C:
		}
		t.Stop()
	}
}

func (s *Store) execute(ctx context.Context, j Job) {
	late := time.Since(j.At)
	status, result := StatusDone, "done"
	var rep *macros.Report
	if late > Grace {
		status, result = StatusMissed, fmt.Sprintf("%v late — not run", late.Round(time.Second))
	} else {
		var err error
		rep, err = s.run(ctx, j)
		switch {
		case err != nil:
			status, result = StatusFailed, err.Error()
		case !rep.OK && rep.Error != nil:
			status, result = StatusFailed, *rep.Error
		}
	}
	s.mu.Lock()
	for i := range s.jobs {
		if s.jobs[i].ID == j.ID {
			t := time.Now()
			s.jobs[i].Status, s.jobs[i].Result, s.jobs[i].Report, s.jobs[i].Ended = status, result, rep, &t
		}
	}
	s.changed()
	s.mu.Unlock()
	lvl := slog.LevelInfo
	if status != StatusDone {
		lvl = slog.LevelWarn
	}
	s.log.Log(ctx, lvl, "scheduled action "+status, "label", j.Label, "result", result, "by", j.By)
}
