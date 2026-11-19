// Package camera owns the single VISCA TCP connection to the camera and
// the camera's HTTP CGI surface.
//
// Smooth-control design (carried over from the Python build):
//
//   - ONE persistent TCP connection, auto-reconnect; frames are written
//     under one lock so concurrent goroutines never interleave bytes.
//   - Drive (press-and-hold) is a state, not a stream. Clients set their
//     desired drive vector; a 25 Hz control loop emits a VISCA command
//     only when the applied command would actually change.
//   - Speed ramps in over ~200 ms at drive start.
//   - Dead-man's switch: a driving client silent > 1.2 s stops the camera.
//   - Discrete commands force-stop any active drive first.
//
// Fixes over the Python build:
//   - The applied-command signature is reset on every stop, so a second
//     press in the same direction at a low speed always re-sends drive
//     (Python skipped it when speed <= ~9, the "Precise" end).
//   - The completion waiter is armed before the command is written, so a
//     fast camera's completion can't be missed (Python cleared after).
//   - A dropped connection is closed, never written to again.
//   - One-push WB uses the real trigger frame (see visca.WBOnePushTrigger).
package camera

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"ptz/internal/config"
	"ptz/internal/digest"
	"ptz/internal/visca"
)

const (
	Tick      = 40 * time.Millisecond // 25 Hz control loop
	DeadMan   = 1200 * time.Millisecond
	RampStep  = 9.0 // speed units (of 100) gained per tick at drive start
	pollEvery = 600 * time.Millisecond

	inquiryTimeout    = 1500 * time.Millisecond
	completionTimeout = 2 * time.Second
	writeTimeout      = 2 * time.Second
	reconnectDelay    = 2 * time.Second

	// Pick-a-white-point flow.
	WBPanSpeed        = 18
	WBTiltSpeed       = 16
	WBSettleTimeout   = 3 * time.Second
	WBSettlePoll      = 200 * time.Millisecond
	WBTriggerSettle   = 1500 * time.Millisecond
	WBAimTolerance    = 24 // ~1.7°
	WBReturnTolerance = 8  // ~0.6°
)

var (
	ErrNotConnected = errors.New("camera not connected")
	ErrBusy         = errors.New("another camera operation is in progress")
)

// DriveVector is a client's desired motion.
type DriveVector struct {
	Pan, Tilt, Zoom int // -1/0/+1 each
	Speed           int // 5..100
}

func (v DriveVector) Active() bool { return v.Pan != 0 || v.Tilt != 0 || v.Zoom != 0 }

// Status is the JSON shape the web UI reads (same keys as Python).
type Status struct {
	Connected bool              `json:"connected"`
	Driving   bool              `json:"driving"`
	Power     *bool             `json:"power"`
	Pan       *int              `json:"pan"`
	Tilt      *int              `json:"tilt"`
	Zoom      *int              `json:"zoom"`
	FocusAuto *bool             `json:"focusAuto"`
	Device    map[string]string `json:"device"`
	Error     string            `json:"error"`
}

type driveSig struct {
	pan, tilt, zoom       int
	panSp, tiltSp, zoomSp int
}

type Service struct {
	cfg  config.Config
	http *http.Client
	log  *slog.Logger

	// OnChange is called (from service goroutines) whenever status
	// changes. Must not block for long.
	OnChange func(Status)

	writeMu sync.Mutex
	connMu  sync.Mutex
	conn    net.Conn

	inqMu      sync.Mutex // one inquiry in flight
	cmdMu      sync.Mutex // one awaited discrete command in flight
	waitMu     sync.Mutex
	replyCh    chan []byte
	completeCh chan struct{}

	mu            sync.Mutex // guards everything below
	st            Status
	desired       DriveVector
	lastDriveMsg  time.Time
	driveOwner    string
	appliedKey    [3]int
	appliedSpeed  float64
	appliedSig    *driveSig
	deviceFetched time.Time

	opLock   sync.Mutex
	opActive atomic.Bool
}

func New(cfg config.Config, log *slog.Logger) *Service {
	var rt http.RoundTripper = http.DefaultTransport
	if cfg.CameraUser != "" {
		rt = &digest.Transport{User: cfg.CameraUser, Pass: cfg.CameraPass}
	}
	return &Service{
		cfg:  cfg,
		http: &http.Client{Transport: rt, Timeout: 5 * time.Second},
		log:  log,
		st:   Status{Device: map[string]string{}},
	}
}

// Start runs the connection, control and poll loops until ctx ends.
func (s *Service) Start(ctx context.Context) {
	go s.connectionLoop(ctx)
	go s.controlLoop(ctx)
	go s.pollLoop(ctx)
}

// OperationActive is true while a sequenced operation owns the camera.
func (s *Service) OperationActive() bool { return s.opActive.Load() }

// Snapshot returns a copy of the current status.
func (s *Service) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Service) snapshotLocked() Status {
	c := s.st
	c.Device = make(map[string]string, len(s.st.Device))
	for k, v := range s.st.Device {
		c.Device[k] = v
	}
	return c
}

func (s *Service) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Connected
}

func (s *Service) Driving() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Driving
}

func (s *Service) notify() {
	if s.OnChange != nil {
		s.OnChange(s.Snapshot())
	}
}

// ------------------------------------------------------------ connection

func (s *Service) connectionLoop(ctx context.Context) {
	addr := net.JoinHostPort(s.cfg.CameraHost, fmt.Sprint(s.cfg.CameraPort))
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	for ctx.Err() == nil {
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			s.connMu.Lock()
			s.conn = conn
			s.connMu.Unlock()
			s.mu.Lock()
			s.st.Connected = true
			s.st.Error = ""
			s.mu.Unlock()
			s.log.Info("VISCA connected", "addr", addr)
			s.notify()
			err = s.readLoop(ctx, conn)
			s.connMu.Lock()
			s.conn = nil
			s.connMu.Unlock()
			conn.Close()
		}
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if err != nil {
			s.st.Error = "connect: " + err.Error()
		}
		s.st.Connected = false
		s.st.Driving = false
		s.appliedKey = [3]int{}
		s.appliedSpeed = 0
		s.appliedSig = nil
		s.mu.Unlock()
		s.notify()
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

func (s *Service) readLoop(ctx context.Context, conn net.Conn) error {
	go func() { <-ctx.Done(); conn.Close() }()
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 4096)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			var frames [][]byte
			frames, buf = visca.SplitFrames(append(buf, tmp[:n]...))
			buf = append([]byte(nil), buf...)
			for _, f := range frames {
				s.route(f)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("camera closed the connection")
			}
			return err
		}
	}
}

func (s *Service) route(f []byte) {
	switch visca.FrameKind(f) {
	case visca.KindReply:
		s.waitMu.Lock()
		if s.replyCh != nil {
			select {
			case s.replyCh <- f:
			default:
			}
		}
		s.waitMu.Unlock()
	case visca.KindCompletion:
		s.waitMu.Lock()
		if s.completeCh != nil {
			select {
			case s.completeCh <- struct{}{}:
			default:
			}
		}
		s.waitMu.Unlock()
	case visca.KindError:
		s.mu.Lock()
		s.st.Error = fmt.Sprintf("VISCA error % x", f)
		s.mu.Unlock()
		s.log.Warn("camera error reply", "frame", fmt.Sprintf("% x", f))
	}
}

// --------------------------------------------------------------- sending

func (s *Service) write(frame []byte) error {
	s.connMu.Lock()
	conn := s.conn
	s.connMu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := conn.Write(frame)
	if err != nil {
		conn.Close() // readLoop sees it and the connection loop reconnects
	}
	return err
}

// SendCommand writes a discrete command and waits briefly for its
// completion. A missing completion is not fatal (cameras don't always
// complete promptly).
func (s *Service) SendCommand(frame []byte, wait bool) error {
	if !wait {
		return s.write(frame)
	}
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	ch := make(chan struct{}, 1)
	s.waitMu.Lock()
	s.completeCh = ch // armed BEFORE the write
	s.waitMu.Unlock()
	defer func() {
		s.waitMu.Lock()
		s.completeCh = nil
		s.waitMu.Unlock()
	}()
	if err := s.write(frame); err != nil {
		return err
	}
	select {
	case <-ch:
	case <-time.After(completionTimeout):
	}
	return nil
}

// Inquire sends an inquiry and returns its reply frame, or nil.
func (s *Service) Inquire(frame []byte) []byte {
	s.inqMu.Lock()
	defer s.inqMu.Unlock()
	if !s.Connected() {
		return nil
	}
	ch := make(chan []byte, 1)
	s.waitMu.Lock()
	s.replyCh = ch
	s.waitMu.Unlock()
	defer func() {
		s.waitMu.Lock()
		s.replyCh = nil
		s.waitMu.Unlock()
	}()
	if err := s.write(frame); err != nil {
		return nil
	}
	select {
	case f := <-ch:
		return f
	case <-time.After(inquiryTimeout):
		return nil
	}
}

// ----------------------------------------------------------------- drive

// SetDrive is latest-wins drive state from a client. Dropped (never
// queued) while a sequenced operation owns the camera.
func (s *Service) SetDrive(v DriveVector, owner string) {
	if s.opActive.Load() {
		return
	}
	v.Speed = clamp(v.Speed, 5, 100)
	s.mu.Lock()
	s.desired = v
	s.lastDriveMsg = time.Now()
	if v.Active() {
		s.driveOwner = owner
	}
	s.mu.Unlock()
}

// ReleaseOwner stops the camera if `owner` is the one driving it
// (called when a WebSocket client disconnects).
func (s *Service) ReleaseOwner(owner string) {
	s.mu.Lock()
	mine := s.driveOwner == owner && s.desired.Active()
	s.mu.Unlock()
	if mine {
		s.SetDrive(DriveVector{}, owner)
	}
}

func (s *Service) controlLoop(ctx context.Context) {
	t := time.NewTicker(Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.controlTick(); err != nil {
				s.log.Debug("control tick", "err", err)
			}
		}
	}
}

func (s *Service) controlTick() error {
	s.mu.Lock()
	now := time.Now()
	desired := s.desired
	if desired.Active() && now.Sub(s.lastDriveMsg) > DeadMan {
		s.log.Warn("dead-man: driving client silent, stopping camera")
		desired = DriveVector{Speed: desired.Speed}
		s.desired = desired
	}
	if !s.st.Connected {
		s.mu.Unlock()
		return nil
	}
	wasDriving := s.st.Driving
	var frames [][]byte
	if !desired.Active() {
		if s.appliedKey != [3]int{} || s.appliedSpeed > 0 {
			frames = append(frames, visca.PanTiltStop(), visca.ZoomStop())
			s.appliedKey = [3]int{}
			s.appliedSpeed = 0
		}
		s.appliedSig = nil // fix: next press always re-sends drive
		s.st.Driving = false
	} else {
		target := float64(clamp(desired.Speed, 5, 100))
		if s.appliedSpeed < target {
			s.appliedSpeed = min(target, s.appliedSpeed+RampStep)
		} else {
			s.appliedSpeed = target
		}
		eff := s.appliedSpeed
		sig := driveSig{
			pan: desired.Pan, tilt: desired.Tilt, zoom: desired.Zoom,
			panSp:  max(1, roundInt(24*eff/100)),
			tiltSp: max(1, roundInt(20*eff/100)),
			zoomSp: max(1, roundInt(7*eff/100)),
		}
		// Only active axes' speeds count, so a pan/tilt speed step never
		// re-sends an identical zoom frame (and vice versa).
		if desired.Pan == 0 && desired.Tilt == 0 {
			sig.panSp, sig.tiltSp = 0, 0
		}
		if desired.Zoom == 0 {
			sig.zoomSp = 0
		}
		if s.appliedSig == nil || *s.appliedSig != sig {
			if desired.Pan != 0 || desired.Tilt != 0 {
				frames = append(frames, visca.PanTiltDrive(desired.Pan, desired.Tilt, sig.panSp, sig.tiltSp))
			} else if s.appliedKey[0] != 0 || s.appliedKey[1] != 0 {
				frames = append(frames, visca.PanTiltStop())
			}
			if desired.Zoom != 0 {
				frames = append(frames, visca.ZoomDrive(desired.Zoom, sig.zoomSp))
			} else if s.appliedKey[2] != 0 {
				frames = append(frames, visca.ZoomStop())
			}
			s.appliedSig = &sig
		}
		s.appliedKey = [3]int{desired.Pan, desired.Tilt, desired.Zoom}
		s.st.Driving = true
	}
	changed := wasDriving != s.st.Driving
	s.mu.Unlock()

	var err error
	for _, f := range frames {
		if e := s.write(f); e != nil {
			err = e
			break
		}
	}
	if changed {
		s.notify()
	}
	return err
}

// ForceStop cancels any drive and stops the camera now.
func (s *Service) ForceStop() {
	s.mu.Lock()
	s.desired = DriveVector{Speed: s.desired.Speed}
	needStop := s.st.Connected && (s.appliedKey != [3]int{} || s.appliedSpeed > 0)
	s.appliedKey = [3]int{}
	s.appliedSpeed = 0
	s.appliedSig = nil
	s.st.Driving = false
	s.mu.Unlock()
	if needStop {
		_ = s.write(visca.PanTiltStop())
		_ = s.write(visca.ZoomStop())
	}
}

// -------------------------------------------------------- discrete moves

func (s *Service) Home() error {
	s.ForceStop()
	return s.SendCommand(visca.Home(), true)
}

func (s *Service) RecallPreset(slot int) error {
	s.ForceStop()
	return s.SendCommand(visca.PresetRecall(slot), true)
}

func (s *Service) StorePreset(slot int) error {
	s.ForceStop()
	return s.SendCommand(visca.PresetSet(slot), true)
}

func (s *Service) ClearPreset(slot int) error {
	s.ForceStop()
	return s.SendCommand(visca.PresetClear(slot), true)
}

// Focus runs one of: auto, manual, onepush, near, far, stop.
func (s *Service) Focus(action string) error {
	var err error
	switch action {
	case "auto", "manual":
		auto := action == "auto"
		if err = s.SendCommand(visca.FocusMode(auto), true); err == nil {
			s.mu.Lock()
			s.st.FocusAuto = &auto
			s.mu.Unlock()
		}
	case "onepush":
		err = s.SendCommand(visca.FocusOnePush(), true)
	case "near":
		err = s.SendCommand(visca.FocusDrive(-1), false)
	case "far":
		err = s.SendCommand(visca.FocusDrive(1), false)
	case "stop":
		err = s.SendCommand(visca.FocusDrive(0), false)
	default:
		return fmt.Errorf("unknown focus action %q", action)
	}
	s.notify()
	return err
}

// ---------------------------------------------------------------- status

func (s *Service) pollLoop(ctx context.Context) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	var lastStatic time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !s.Connected() {
			lastStatic = time.Time{} // refresh again as soon as it reconnects
			continue
		}
		// Power, focus mode and device info: on connect, then every 30 s.
		if time.Since(lastStatic) > 30*time.Second {
			lastStatic = time.Now()
			sctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			s.RefreshStatic(sctx)
			cancel()
			s.notify()
		}
		changed := false
		if f := s.Inquire(visca.InqPanTiltPos); f != nil {
			if p, tl, ok := visca.ParsePanTiltPos(f); ok {
				s.mu.Lock()
				if s.st.Pan == nil || *s.st.Pan != p || *s.st.Tilt != tl {
					s.st.Pan, s.st.Tilt = &p, &tl
					changed = true
				}
				s.mu.Unlock()
			}
		}
		if f := s.Inquire(visca.InqZoomPos); f != nil {
			if z, ok := visca.ParseZoomPos(f); ok {
				s.mu.Lock()
				if s.st.Zoom == nil || *s.st.Zoom != z {
					s.st.Zoom = &z
					changed = true
				}
				s.mu.Unlock()
			}
		}
		if changed {
			s.notify()
		}
	}
}

// RefreshStatic refreshes power + focus mode (VISCA) and device info
// (CGI, cached 30 s).
func (s *Service) RefreshStatic(ctx context.Context) {
	if s.Connected() {
		if f := s.Inquire(visca.InqPower); f != nil {
			if on, ok := visca.ParsePower(f); ok {
				s.mu.Lock()
				s.st.Power = &on
				s.mu.Unlock()
			}
		}
		if f := s.Inquire(visca.InqFocusMode); f != nil {
			if a, ok := visca.ParseFocusMode(f); ok {
				s.mu.Lock()
				s.st.FocusAuto = &a
				s.mu.Unlock()
			}
		}
	}
	s.mu.Lock()
	stale := time.Since(s.deviceFetched) > 30*time.Second || len(s.st.Device) == 0
	s.mu.Unlock()
	if !stale {
		return
	}
	body, err := s.HTTPGet(ctx, "/cgi-bin/param.cgi?get_device_conf")
	if err != nil {
		s.log.Debug("device_conf fetch failed", "err", err)
		return
	}
	dev := map[string]string{}
	for _, m := range confRe.FindAllSubmatch(body, -1) {
		dev[string(m[1])] = string(m[2])
	}
	s.mu.Lock()
	s.st.Device = dev
	s.deviceFetched = time.Now()
	s.mu.Unlock()
}

var confRe = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)

// HTTPGet fetches a camera path (CGI / snapshot) with digest auth when
// credentials are configured.
func (s *Service) HTTPGet(ctx context.Context, pathAndQuery string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.CameraHTTP+pathAndQuery, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("camera HTTP %d for %s", resp.StatusCode, pathAndQuery)
	}
	return b, nil
}

func (s *Service) FetchSnapshot(ctx context.Context) ([]byte, error) {
	b, err := s.HTTPGet(ctx, "/snapshot.jpg")
	if err == nil && !bytes.HasPrefix(b, []byte{0xFF, 0xD8}) && len(b) > 0 {
		s.log.Debug("snapshot is not a JPEG", "len", len(b))
	}
	return b, err
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func roundInt(f float64) int {
	if f < 0 {
		return -int(-f + 0.5)
	}
	return int(f + 0.5)
}
