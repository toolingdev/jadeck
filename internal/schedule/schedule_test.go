package schedule

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ptz/internal/macros"
)

func TestRunsCancelsAndMisses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	var mu sync.Mutex
	var ran []string
	run := func(_ context.Context, j Job) (*macros.Report, error) {
		mu.Lock()
		ran = append(ran, j.Label)
		mu.Unlock()
		return &macros.Report{OK: true}, nil
	}
	log := slog.New(slog.DiscardHandler)
	s, err := Open(path, run, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	cut := []macros.Step{{Type: "switcher", Action: "cut"}}
	soon, _ := s.Add(Job{At: time.Now().Add(150 * time.Millisecond), Label: "cut soon", Steps: cut, By: "test"})
	later, _ := s.Add(Job{At: time.Now().Add(time.Hour), Label: "later", Steps: cut, By: "test"})
	gone, _ := s.Add(Job{At: time.Now().Add(200 * time.Millisecond), Label: "cancelled", Steps: cut, By: "test"})
	if err := s.Cancel(gone.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Job{At: time.Now().Add(48 * time.Hour), Steps: cut}); err == nil {
		t.Fatal("2 days ahead accepted")
	}
	if _, err := s.Add(Job{At: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("empty job accepted")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(ran)
		mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if len(ran) != 1 || ran[0] != "cut soon" {
		t.Fatalf("ran %v", ran)
	}
	mu.Unlock()
	st := map[string]string{}
	for _, j := range s.List() {
		st[j.ID] = j.Status
	}
	if st[soon.ID] != StatusDone || st[gone.ID] != StatusCancelled || st[later.ID] != StatusPending {
		t.Fatalf("statuses %v", st)
	}
	// Run now pulls a job forward.
	if err := s.RunNow(later.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if len(ran) != 2 {
		t.Fatalf("run now: ran %v", ran)
	}
	mu.Unlock()
	cancel()

	// A job whose time passed while the controller was off is missed on start.
	s2, _ := Open(filepath.Join(t.TempDir(), "s.json"), run, log)
	s2.jobs = append(s2.jobs, Job{ID: "x", At: time.Now().Add(-10 * time.Minute), Status: StatusPending, Label: "old", Steps: cut})
	s2.saveLocked()
	s3, _ := Open(s2.path, run, log)
	if l := s3.List(); len(l) != 1 || l[0].Status != StatusMissed {
		t.Fatalf("after restart: %+v", l)
	}
}
