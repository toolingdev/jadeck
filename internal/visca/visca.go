// Package visca builds VISCA command frames and parses reply frames for
// the PTZOptics G2 (V63C) camera: raw VISCA over TCP (port 5678),
// camera address 1 (0x81). Pure functions only; no I/O.
//
// Byte templates were cross-checked against the legacy app's inline
// templates (recon/visca-findings.txt) and the PTZOptics G2 VISCA
// command list.
package visca

import "math"

const (
	Cam  = 0x81 // camera address 1
	Term = 0xFF
)

// Position model. Absolute pan/tilt are signed 16-bit camera step
// units. Mechanical spans: pan ±170°, tilt -30..+90°. Both axes share
// one scale: 14.4 units per degree.
const (
	PanMin, PanMax   = -2448, 2448
	TiltMin, TiltMax = -432, 1296
	UnitsPerDegree   = 14.4

	// Zoom: 0x0000 full wide .. 0x4000 full optical tele (30x).
	ZoomOpticalMax = 0x4000
	HFOVWideDeg    = 60.7
	HFOVTeleDeg    = 3.4
)

func cmd(body ...byte) []byte {
	out := make([]byte, 0, len(body)+2)
	out = append(out, Cam)
	out = append(out, body...)
	return append(out, Term)
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

// ---------------------------------------------------------------- drive

// PanTiltDrive is a continuous drive. pan: -1 left, 0 stop, +1 right.
// tilt: -1 down, 0 stop, +1 up. Speeds: pan 1..24, tilt 1..20.
func PanTiltDrive(pan, tilt, panSpeed, tiltSpeed int) []byte {
	x := map[int]byte{-1: 0x01, 0: 0x03, 1: 0x02}[sign(pan)]
	y := map[int]byte{1: 0x01, 0: 0x03, -1: 0x02}[sign(tilt)]
	return cmd(0x01, 0x06, 0x01,
		byte(clamp(panSpeed, 1, 24)), byte(clamp(tiltSpeed, 1, 20)), x, y)
}

// PanTiltStop stops pan/tilt. Speed bytes are carried but ignored by
// the camera for a stop.
func PanTiltStop() []byte {
	return cmd(0x01, 0x06, 0x01, 0x10, 0x10, 0x03, 0x03)
}

func Home() []byte { return cmd(0x01, 0x06, 0x04) }

func posNibbles(v int) []byte {
	u := uint16(int16(v))
	return []byte{byte(u>>12) & 0xF, byte(u>>8) & 0xF, byte(u>>4) & 0xF, byte(u) & 0xF}
}

// PanTiltAbsolute: 81 01 06 02 VV WW 0Y0Y0Y0Y 0Z0Z0Z0Z FF. Positions
// clamp to the mechanical ranges. Zoom is untouched.
func PanTiltAbsolute(pan, tilt, panSpeed, tiltSpeed int) []byte {
	pan = clamp(pan, PanMin, PanMax)
	tilt = clamp(tilt, TiltMin, TiltMax)
	b := []byte{0x01, 0x06, 0x02,
		byte(clamp(panSpeed, 1, 24)), byte(clamp(tiltSpeed, 1, 20))}
	b = append(b, posNibbles(pan)...)
	b = append(b, posNibbles(tilt)...)
	return cmd(b...)
}

// ZoomDrive: direction +1 tele, -1 wide, 0 stop. speed 0..7.
func ZoomDrive(direction, speed int) []byte {
	if direction == 0 {
		return ZoomStop()
	}
	p := byte(clamp(speed, 0, 7))
	if direction > 0 {
		return cmd(0x01, 0x04, 0x07, 0x20|p)
	}
	return cmd(0x01, 0x04, 0x07, 0x30|p)
}

func ZoomStop() []byte { return cmd(0x01, 0x04, 0x07, 0x00) }

// --------------------------------------------------------- white balance

// WBModes are CAM_WB mode values (81 01 04 35 pq FF).
var WBModes = map[string]byte{
	"auto": 0x00, "indoor": 0x01, "outdoor": 0x02,
	"onepush": 0x03, "atw": 0x04, "manual": 0x05,
}

// WBMode sets the white-balance mode. VISCA is the WB source of truth
// on this camera: firmware 6.3.45's get_image_conf has no WB keys.
func WBMode(mode string) ([]byte, bool) {
	v, ok := WBModes[mode]
	if !ok {
		return nil, false
	}
	return cmd(0x01, 0x04, 0x35, v), true
}

// WBOnePushTrigger fires one-push white balance (CAM_WB One Push
// Trigger, 81 01 04 10 05 FF).
//
// Fixes the Python build, which sent 81 01 04 35 05 FF — that is the
// CAM_WB *mode* command with value 0x05, i.e. "set WB to Manual", not a
// trigger. Byte-confirm against the real camera in the first live
// motion window.
func WBOnePushTrigger() []byte { return cmd(0x01, 0x04, 0x10, 0x05) }

// ------------------------------------------------------ wb-pick geometry

// HFOVDegrees is the horizontal field of view at a zoom position,
// interpolated on a log curve between the wide and tele endpoints.
func HFOVDegrees(zoomPos int) float64 {
	frac := float64(clamp(zoomPos, 0, ZoomOpticalMax)) / ZoomOpticalMax
	return math.Exp(math.Log(HFOVWideDeg) +
		frac*(math.Log(HFOVTeleDeg)-math.Log(HFOVWideDeg)))
}

// VFOVDegrees derives vertical FOV from horizontal for a 16:9 sensor.
func VFOVDegrees(hfov float64) float64 {
	return 2 * math.Atan(math.Tan(hfov*math.Pi/360)*9/16) * 180 / math.Pi
}

// WBTarget returns the absolute pan/tilt that brings the tapped point
// (x, y normalized 0..1, origin top-left) to frame center, clamped to
// mechanical limits. Assumes flip/mirror off.
func WBTarget(pan, tilt, zoomPos int, x, y float64) (int, int) {
	h := HFOVDegrees(zoomPos)
	v := VFOVDegrees(h)
	dPan := int(math.Round((x - 0.5) * h * UnitsPerDegree))
	dTilt := int(math.Round(-(y - 0.5) * v * UnitsPerDegree))
	return clamp(pan+dPan, PanMin, PanMax), clamp(tilt+dTilt, TiltMin, TiltMax)
}

// ---------------------------------------------------------------- focus

// FocusDrive: +1 far, -1 near, 0 stop.
func FocusDrive(direction int) []byte {
	return cmd(0x01, 0x04, 0x08, map[int]byte{1: 0x02, -1: 0x03, 0: 0x00}[sign(direction)])
}

func FocusMode(auto bool) []byte {
	if auto {
		return cmd(0x01, 0x04, 0x38, 0x02)
	}
	return cmd(0x01, 0x04, 0x38, 0x03)
}

func FocusOnePush() []byte { return cmd(0x01, 0x04, 0x18, 0x01) }

// -------------------------------------------------------------- presets

// The slot byte is the preset number itself (1..127), matching how the
// legacy app addresses slots. Still to be confirmed byte-for-byte in a
// live motion window; presets are never recalled blind before that.
func preset(mode byte, slot int) []byte {
	return cmd(0x01, 0x04, 0x3F, mode, byte(clamp(slot, 0, 127)))
}

func PresetClear(slot int) []byte  { return preset(0, slot) }
func PresetSet(slot int) []byte    { return preset(1, slot) }
func PresetRecall(slot int) []byte { return preset(2, slot) }

// ------------------------------------------------------------ inquiries

var (
	InqPower      = cmd(0x09, 0x04, 0x00)
	InqPanTiltPos = cmd(0x09, 0x06, 0x12)
	InqZoomPos    = cmd(0x09, 0x04, 0x47)
	InqFocusPos   = cmd(0x09, 0x04, 0x48)
	InqFocusMode  = cmd(0x09, 0x04, 0x38)
)

// -------------------------------------------------------------- parsing

// SplitFrames splits a byte stream into complete 0xFF-terminated frames
// and returns the unconsumed remainder.
func SplitFrames(buf []byte) (frames [][]byte, rest []byte) {
	for {
		i := -1
		for j, b := range buf {
			if b == Term {
				i = j
				break
			}
		}
		if i < 0 {
			return frames, buf
		}
		f := make([]byte, i+1)
		copy(f, buf[:i+1])
		frames = append(frames, f)
		buf = buf[i+1:]
	}
}

type Kind int

const (
	KindUnknown Kind = iota
	KindAck
	KindCompletion
	KindReply
	KindError
)

// FrameKind classifies a reply frame.
func FrameKind(f []byte) Kind {
	if len(f) < 3 || f[0]&0xF0 != 0x90 {
		return KindUnknown
	}
	switch f[1] & 0xF0 {
	case 0x40:
		return KindAck
	case 0x50:
		// 3-byte 9x 5y FF is a completion; longer is an inquiry reply.
		if len(f) == 3 {
			return KindCompletion
		}
		return KindReply
	case 0x60:
		return KindError
	}
	return KindUnknown
}

func nibbleValue(f []byte, start, count int) int {
	v := 0
	for i := 0; i < count; i++ {
		v = v<<4 | int(f[start+i]&0x0F)
	}
	return v
}

func signed16(v int) int { return int(int16(uint16(v))) }

func ParsePower(f []byte) (on bool, ok bool) {
	if FrameKind(f) == KindReply && len(f) >= 4 {
		return f[2] == 0x02, true
	}
	return false, false
}

func ParseZoomPos(f []byte) (int, bool) {
	if FrameKind(f) == KindReply && len(f) >= 7 {
		return nibbleValue(f, 2, 4), true
	}
	return 0, false
}

func ParsePanTiltPos(f []byte) (pan, tilt int, ok bool) {
	if FrameKind(f) == KindReply && len(f) >= 11 {
		return signed16(nibbleValue(f, 2, 4)), signed16(nibbleValue(f, 6, 4)), true
	}
	return 0, 0, false
}

// ParseFocusMode: 02 = auto, 03 = manual.
func ParseFocusMode(f []byte) (auto bool, ok bool) {
	if FrameKind(f) == KindReply && len(f) >= 4 {
		return f[2] == 0x02, true
	}
	return false, false
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
