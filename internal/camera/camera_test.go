package camera

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"ptz/internal/config"
	"ptz/internal/mockcam"
)

func setup(t *testing.T) (*Service, *mockcam.Mock) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := mockcam.New()
	m.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	va, ha, err := m.Serve(ctx, "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(va)
	p, _ := strconv.Atoi(port)
	cfg := config.Config{CameraHost: host, CameraPort: p, CameraHTTP: "http://" + ha}
	s := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Start(ctx)
	waitFor(t, "connect", s.Connected)
	return s, m
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func count(log []string, prefix string) int {
	n := 0
	for _, l := range log {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

const (
	ptDrive = "81010601"
	zoomCmd = "81010407"
)

// A 1 s hold with chatty clients produces only ramp steps + stop.
func TestDriveCoalescing(t *testing.T) {
	s, m := setup(t)
	m.Reset(nil, nil, nil)
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) { // ~400 updates/s of the same vector
		s.SetDrive(DriveVector{Pan: 1, Speed: 50}, "a")
		time.Sleep(2500 * time.Microsecond)
	}
	s.SetDrive(DriveVector{}, "a")
	time.Sleep(200 * time.Millisecond)
	log := m.State().Log
	n := count(log, ptDrive)
	if n < 3 || n > 10 {
		t.Fatalf("expected a handful of pan/tilt frames, got %d: %v", n, log)
	}
	if last := log[len(log)-2]; last != "8101060110100303ff" {
		t.Fatalf("expected a stop near the end, log=%v", log)
	}
	if m.State().Pan <= 0 {
		t.Fatal("camera did not move right")
	}
}

// Regression: Python skipped the second press at low speed because the
// applied signature survived the stop.
func TestLowSpeedRepressSendsDrive(t *testing.T) {
	s, m := setup(t)
	for press := 0; press < 2; press++ {
		m.Reset(nil, nil, nil)
		s.SetDrive(DriveVector{Pan: 1, Speed: 5}, "a")
		time.Sleep(150 * time.Millisecond)
		s.SetDrive(DriveVector{}, "a")
		time.Sleep(150 * time.Millisecond)
		log := m.State().Log
		if count(log, "810106010101020") == 0 { // drive right @1/1
			t.Fatalf("press %d: no drive frame sent: %v", press+1, log)
		}
	}
}

func TestDeadMan(t *testing.T) {
	s, m := setup(t)
	m.Reset(nil, nil, nil)
	s.SetDrive(DriveVector{Tilt: 1, Speed: 60}, "a")
	time.Sleep(DeadMan + 300*time.Millisecond) // client goes silent
	if s.Driving() {
		t.Fatal("still driving after dead-man timeout")
	}
	log := m.State().Log
	if log[len(log)-2] != "8101060110100303ff" {
		t.Fatalf("no stop: %v", log)
	}
}

func TestZoomOnlyAndRelease(t *testing.T) {
	s, m := setup(t)
	m.Reset(nil, nil, nil)
	s.SetDrive(DriveVector{Zoom: 1, Speed: 100}, "a")
	time.Sleep(700 * time.Millisecond) // full ramp is ~12 ticks
	s.ReleaseOwner("someone-else")
	if !s.Driving() {
		t.Fatal("another owner's release stopped the drive")
	}
	s.ReleaseOwner("a")
	time.Sleep(150 * time.Millisecond)
	if s.Driving() {
		t.Fatal("owner release did not stop")
	}
	log := m.State().Log
	if count(log, zoomCmd+"27") != 1 || count(log, ptDrive) != 1 {
		t.Fatalf("expected ramp to zoom tele 7 (sent once), then stops: %v", log)
	}
	for i := 1; i < len(log); i++ {
		if log[i] == log[i-1] {
			t.Fatalf("duplicate consecutive frame %s: %v", log[i], log)
		}
	}
}

func TestPresetsAndHome(t *testing.T) {
	s, m := setup(t)
	pan, tilt := 500, 200
	m.Reset(&pan, &tilt, nil)
	if err := s.StorePreset(3); err != nil {
		t.Fatal(err)
	}
	if err := s.Home(); err != nil {
		t.Fatal(err)
	}
	if st := m.State(); st.Pan != 0 || st.Tilt != 0 {
		t.Fatalf("home: %+v", st)
	}
	if err := s.RecallPreset(3); err != nil {
		t.Fatal(err)
	}
	if st := m.State(); st.Pan != 500 || st.Tilt != 200 {
		t.Fatalf("recall: %+v", st)
	}
}

func TestReconnect(t *testing.T) {
	s, m := setup(t)
	m.DropConnections()
	waitFor(t, "disconnect", func() bool { return !s.Connected() })
	waitFor(t, "reconnect", s.Connected)
	if err := s.Home(); err != nil {
		t.Fatalf("command after reconnect: %v", err)
	}
}

func TestWBPickSequence(t *testing.T) {
	s, m := setup(t)
	pan, tilt, zoom := 100, 50, 0
	m.Reset(&pan, &tilt, &zoom)
	waitFor(t, "poll", func() bool { st := s.Snapshot(); return st.Zoom != nil && *st.Zoom == 0 })
	res, err := s.WBPick(0.9, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	st := m.State()
	if st.WBVISCATriggers != 1 {
		t.Fatalf("triggers=%d log=%v", st.WBVISCATriggers, st.Log)
	}
	if st.WBModeByte == nil || *st.WBModeByte != 0x03 {
		t.Fatalf("WB mode should be onepush (03), got %v", st.WBModeByte)
	}
	if st.Pan != 100 || st.Tilt != 50 {
		t.Fatalf("not returned: %d,%d", st.Pan, st.Tilt)
	}
	if res.AimedAt.Pan <= 100 || res.AimedAt.Tilt <= 50 {
		t.Fatalf("aim should be right+up: %+v", res.AimedAt)
	}
	if st.AbsSeen != 2 {
		t.Fatalf("expected aim + return moves, got %d", st.AbsSeen)
	}
}

func TestWBPickFailedAimStillReturns(t *testing.T) {
	s, m := setup(t)
	pan, tilt, zoom := 0, 0, 0
	m.Reset(&pan, &tilt, &zoom)
	waitFor(t, "poll", func() bool { st := s.Snapshot(); return st.Zoom != nil && *st.Zoom == 0 })
	m.DropAbs(1) // aim is ACKed but never happens
	_, err := s.WBPick(0.1, 0.9)
	var we *WBPickError
	if !errors.As(err, &we) || !strings.Contains(err.Error(), "aim") {
		t.Fatalf("expected aim error, got %v", err)
	}
	if st := m.State(); st.WBVISCATriggers != 0 || st.AbsSeen != 2 {
		t.Fatalf("must not trigger, must attempt return: %+v", st)
	}
}

func TestWBPickBusyAndDriveDropped(t *testing.T) {
	s, m := setup(t)
	zoom := 0
	m.Reset(nil, nil, &zoom)
	waitFor(t, "poll", func() bool { st := s.Snapshot(); return st.Zoom != nil && *st.Zoom == 0 })
	done := make(chan error, 1)
	go func() { _, err := s.WBPick(0.8, 0.5); done <- err }()
	waitFor(t, "op active", s.OperationActive)
	if _, err := s.WBPick(0.5, 0.5); !errors.Is(err, ErrBusy) {
		t.Fatalf("second pick: %v", err)
	}
	s.SetDrive(DriveVector{Pan: -1, Speed: 100}, "x")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.Driving() {
		t.Fatal("drive input leaked into the sequence")
	}
}

func TestDeviceConf(t *testing.T) {
	s, _ := setup(t)
	s.RefreshStatic(context.Background())
	st := s.Snapshot()
	if st.Device["devtype"] != "V63C" || st.Power == nil || !*st.Power {
		t.Fatalf("%+v", st)
	}
}
