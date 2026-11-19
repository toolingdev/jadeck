package camera

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ptz/internal/visca"
)

// WBPickError means the pick-a-white-point flow could not complete.
type WBPickError struct{ Msg string }

func (e *WBPickError) Error() string { return e.Msg }

type Pos struct {
	Pan  int  `json:"pan"`
	Tilt int  `json:"tilt"`
	Zoom *int `json:"zoom,omitempty"`
}

type WBPickResult struct {
	OK         bool               `json:"ok"`
	Tapped     map[string]float64 `json:"tapped"`
	From       Pos                `json:"from"`
	AimedAt    Pos                `json:"aimedAt"`
	ReturnedTo Pos                `json:"returnedTo"`
}

// readPosition returns fresh (pan, tilt, zoom) from inquiries, falling
// back to the continuously polled values.
func (s *Service) readPosition() (pan, tilt, zoom int, ok bool) {
	var hp, hz bool
	if f := s.Inquire(visca.InqPanTiltPos); f != nil {
		pan, tilt, hp = visca.ParsePanTiltPos(f)
	}
	if f := s.Inquire(visca.InqZoomPos); f != nil {
		zoom, hz = visca.ParseZoomPos(f)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !hp && s.st.Pan != nil && s.st.Tilt != nil {
		pan, tilt, hp = *s.st.Pan, *s.st.Tilt, true
	}
	if !hz && s.st.Zoom != nil {
		zoom, hz = *s.st.Zoom, true
	}
	return pan, tilt, zoom, hp && hz
}

// waitSettled polls until two reads ~200 ms apart agree, then requires
// the result within tolerance of target.
func (s *Service) waitSettled(tp, tt, tol int, what string) (int, int, error) {
	deadline := time.Now().Add(WBSettleTimeout)
	var prev, last *[2]int
	for {
		if f := s.Inquire(visca.InqPanTiltPos); f != nil {
			if p, t, ok := visca.ParsePanTiltPos(f); ok {
				cur := [2]int{p, t}
				last = &cur
				if prev != nil && *prev == cur {
					break
				}
				prev = &cur
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(WBSettlePoll)
	}
	if last == nil {
		return 0, 0, &WBPickError{"camera stopped answering during " + what}
	}
	if abs(last[0]-tp) > tol || abs(last[1]-tt) > tol {
		return 0, 0, &WBPickError{fmt.Sprintf(
			"camera did not reach the %s position (at %d,%d, wanted %d,%d)",
			what, last[0], last[1], tp, tt)}
	}
	return last[0], last[1], nil
}

// WBPick aims the tapped point (normalized 0..1, origin top-left) at
// frame center, fires One Push WB over VISCA, and returns the camera to
// exactly where it was. Zoom is never touched. Holds the operation lock
// for the whole flow; on any failure after motion began, the return
// move is still attempted before the error is reported.
func (s *Service) WBPick(x, y float64) (*WBPickResult, error) {
	if !s.Connected() {
		return nil, ErrNotConnected
	}
	if !s.opLock.TryLock() {
		return nil, ErrBusy
	}
	s.opActive.Store(true)
	defer func() { s.opActive.Store(false); s.opLock.Unlock() }()

	s.ForceStop()
	pan0, tilt0, zoom0, ok := s.readPosition()
	if !ok {
		return nil, &WBPickError{"could not read the camera position"}
	}
	tp, tt := visca.WBTarget(pan0, tilt0, zoom0, x, y)
	moved := tp != pan0 || tt != tilt0

	goBack := func() (int, int, error) {
		if !moved {
			return pan0, tilt0, nil
		}
		if err := s.SendCommand(visca.PanTiltAbsolute(pan0, tilt0, WBPanSpeed, WBTiltSpeed), true); err != nil {
			return 0, 0, err
		}
		return s.waitSettled(pan0, tilt0, WBReturnTolerance, "return")
	}

	err := func() error {
		mode, _ := visca.WBMode("onepush")
		if err := s.SendCommand(mode, true); err != nil {
			return err
		}
		if moved {
			if err := s.SendCommand(visca.PanTiltAbsolute(tp, tt, WBPanSpeed, WBTiltSpeed), true); err != nil {
				return err
			}
			if _, _, err := s.waitSettled(tp, tt, WBAimTolerance, "aim"); err != nil {
				return err
			}
		}
		if err := s.SendCommand(visca.WBOnePushTrigger(), true); err != nil {
			return err
		}
		time.Sleep(WBTriggerSettle)
		return nil
	}()
	if err != nil {
		_, _, _ = goBack() // best effort before reporting
		var we *WBPickError
		if errors.As(err, &we) {
			return nil, err
		}
		return nil, &WBPickError{"white-balance pick failed: " + err.Error()}
	}
	fp, ft, err := goBack()
	if err != nil {
		return nil, err
	}
	s.notify()
	z := zoom0
	return &WBPickResult{
		OK:         true,
		Tapped:     map[string]float64{"x": x, "y": y},
		From:       Pos{Pan: pan0, Tilt: tilt0, Zoom: &z},
		AimedAt:    Pos{Pan: tp, Tilt: tt},
		ReturnedTo: Pos{Pan: fp, Tilt: ft},
	}, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// MoveRelative pans/tilts by a bounded number of degrees with an
// absolute move and waits for the camera to settle. Used by agents
// (MCP), which get bounded motion only — never an open-ended drive.
func (s *Service) MoveRelative(panDeg, tiltDeg float64) (from, to Pos, err error) {
	if panDeg < -90 || panDeg > 90 || tiltDeg < -60 || tiltDeg > 60 {
		return from, to, &WBPickError{"a single move is limited to ±90° pan and ±60° tilt"}
	}
	if !s.Connected() {
		return from, to, ErrNotConnected
	}
	if !s.opLock.TryLock() {
		return from, to, ErrBusy
	}
	s.opActive.Store(true)
	defer func() { s.opActive.Store(false); s.opLock.Unlock() }()
	s.ForceStop()
	p0, t0, _, ok := s.readPosition()
	if !ok {
		return from, to, &WBPickError{"could not read the camera position"}
	}
	tp := clamp(p0+int(panDeg*visca.UnitsPerDegree), visca.PanMin, visca.PanMax)
	tt := clamp(t0+int(tiltDeg*visca.UnitsPerDegree), visca.TiltMin, visca.TiltMax)
	from = Pos{Pan: p0, Tilt: t0}
	if tp == p0 && tt == t0 {
		return from, from, nil
	}
	if err := s.SendCommand(visca.PanTiltAbsolute(tp, tt, WBPanSpeed, WBTiltSpeed), true); err != nil {
		return from, to, err
	}
	fp, ft, err := s.waitSettled(tp, tt, WBAimTolerance, "target")
	s.notify()
	return from, Pos{Pan: fp, Tilt: ft}, err
}

// ZoomFor zooms in (+1) or out (-1) for a bounded time, then stops.
func (s *Service) ZoomFor(ctx context.Context, dir int, d time.Duration, speed int) error {
	if d <= 0 || d > 3*time.Second {
		return &WBPickError{"zoom time must be 1-3000 ms"}
	}
	if !s.Connected() {
		return ErrNotConnected
	}
	if s.OperationActive() {
		return ErrBusy
	}
	const owner = "agent-zoom"
	end := time.Now().Add(d)
	for time.Now().Before(end) && ctx.Err() == nil {
		s.SetDrive(DriveVector{Zoom: dir, Speed: speed}, owner) // keepalive under the dead-man
		time.Sleep(min(300*time.Millisecond, time.Until(end)))
	}
	s.SetDrive(DriveVector{}, owner)
	time.Sleep(2 * Tick)
	return ctx.Err()
}
