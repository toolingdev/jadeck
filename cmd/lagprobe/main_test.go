package main

import (
	"testing"
	"time"
)

// A camera that starts turning 120 ms after the command at 20°/s,
// coasts 80 ms after the stop, and whose video shows it 200 ms later.
func TestAnalyze(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := t0.Add(1500 * time.Millisecond)
	end := t1.Add(2 * time.Second)
	moveAt, stopAt := t0.Add(120*time.Millisecond), t1.Add(80*time.Millisecond)
	panAtT := func(tt time.Time) int {
		switch {
		case tt.Before(moveAt):
			return 0
		case tt.Before(stopAt):
			return int(tt.Sub(moveAt).Seconds() * 20 * 14.4)
		}
		return int(stopAt.Sub(moveAt).Seconds() * 20 * 14.4)
	}
	var ps []posSample
	for tt := t0.Add(-time.Second); tt.Before(end); tt = tt.Add(20 * time.Millisecond) {
		ps = append(ps, posSample{t: tt, pan: panAtT(tt)})
	}
	var vs []vidSample
	for tt := t0.Add(-1500 * time.Millisecond); tt.Before(end); tt = tt.Add(33 * time.Millisecond) {
		d := 0.2 + float64(tt.UnixNano()%3)*0.01
		seen := tt.Add(-200 * time.Millisecond)
		if !seen.Before(moveAt) && seen.Before(stopAt) {
			d = 4
		}
		vs = append(vs, vidSample{tt, d})
	}
	r := analyze(ps, vs, t0, t1, end, 8, 1)
	near := func(name string, got, want, tol float64) {
		if got < want-tol || got > want+tol {
			t.Errorf("%s = %v, want %v ± %v", name, got, want, tol)
		}
	}
	near("PosStart", r.PosStart, 120, 25)
	near("DegPerSec", r.DegPerSec, 20, 0.5)
	near("PosStop", r.PosStop, 80, 25)
	near("CoastDeg", r.CoastDeg, 1.6, 0.2)
	near("VidStart", r.VidStart, 320, 40)
	near("VidDelay", r.VidDelay, 200, 45)
	near("VidStop", r.VidStop, 280, 40)
	if r.Notes != "" {
		t.Errorf("notes: %s", r.Notes)
	}
}
