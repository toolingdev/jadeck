package visca

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func h(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestCommandFrames(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"drive right+up", PanTiltDrive(1, 1, 12, 10), "8101060 10c0a0201ff"},
		{"drive left+down", PanTiltDrive(-1, -1, 30, 30), "81010601181401 02ff"},
		{"stop", PanTiltStop(), "8101060110100303ff"},
		{"home", Home(), "81010604ff"},
		{"zoom tele 7", ZoomDrive(1, 7), "8101040727ff"},
		{"zoom wide 3", ZoomDrive(-1, 3), "8101040733ff"},
		{"zoom stop", ZoomDrive(0, 5), "8101040700ff"},
		{"focus far", FocusDrive(1), "8101040802ff"},
		{"focus near", FocusDrive(-1), "8101040803ff"},
		{"focus auto", FocusMode(true), "8101043802ff"},
		{"focus manual", FocusMode(false), "8101043803ff"},
		{"focus onepush", FocusOnePush(), "8101041801ff"},
		{"preset set 5", PresetSet(5), "8101043f0105ff"},
		{"preset recall 127", PresetRecall(127), "8101043f027fff"},
		{"preset clear 1", PresetClear(1), "8101043f0001ff"},
		{"wb onepush trigger", WBOnePushTrigger(), "8101041005ff"},
		{"abs -1,-1", PanTiltAbsolute(-1, -1, 18, 16),
			"810106021210" + "0f0f0f0f" + "0f0f0f0f" + "ff"},
	}
	for _, c := range cases {
		want := h(stripSpaces(c.want))
		if !bytes.Equal(c.got, want) {
			t.Errorf("%s: got % x want % x", c.name, c.got, want)
		}
	}
}

func stripSpaces(s string) string {
	return string(bytes.ReplaceAll([]byte(s), []byte(" "), nil))
}

func TestWBModeIsNotTrigger(t *testing.T) {
	manual, _ := WBMode("manual")
	if bytes.Equal(manual, WBOnePushTrigger()) {
		t.Fatal("one-push trigger must not equal the WB=manual mode frame")
	}
	if _, ok := WBMode("bogus"); ok {
		t.Fatal("unknown WB mode accepted")
	}
}

func TestAbsoluteClamps(t *testing.T) {
	f := PanTiltAbsolute(99999, -99999, 99, 99)
	pan, tilt := signed16(nibbleValue(f, 6, 4)), signed16(nibbleValue(f, 10, 4))
	if pan != PanMax || tilt != TiltMin {
		t.Fatalf("clamp: got %d,%d", pan, tilt)
	}
	if f[4] != 24 || f[5] != 20 {
		t.Fatalf("speed clamp: % x", f)
	}
}

func TestSplitFrames(t *testing.T) {
	fr, rest := SplitFrames(h("9041ff9051ff90500203"))
	if len(fr) != 2 || !bytes.Equal(rest, h("90500203")) {
		t.Fatalf("frames=%d rest=% x", len(fr), rest)
	}
}

func TestParsers(t *testing.T) {
	if FrameKind(h("9041ff")) != KindAck || FrameKind(h("9051ff")) != KindCompletion ||
		FrameKind(h("906002ff")) != KindError || FrameKind(h("905002ff")) != KindReply {
		t.Fatal("frame kinds")
	}
	// pan -2 (fffe), tilt 300 (012c)
	p, tl, ok := ParsePanTiltPos(h("90500f0f0f0e00010" + "20cff"))
	if !ok || p != -2 || tl != 300 {
		t.Fatalf("pantilt %d %d %v", p, tl, ok)
	}
	z, ok := ParseZoomPos(h("905004000000ff"))
	if !ok || z != 0x4000 {
		t.Fatalf("zoom %x", z)
	}
	if on, ok := ParsePower(h("905002ff")); !ok || !on {
		t.Fatal("power")
	}
	if a, ok := ParseFocusMode(h("905003ff")); !ok || a {
		t.Fatal("focus mode")
	}
}

func TestWBTargetGeometry(t *testing.T) {
	// Center tap: no motion.
	if p, tl := WBTarget(100, 200, 0, 0.5, 0.5); p != 100 || tl != 200 {
		t.Fatalf("center moved: %d %d", p, tl)
	}
	// Right edge at full wide: half the HFOV to the right.
	p, _ := WBTarget(0, 0, 0, 1.0, 0.5)
	hw := 0.5 * HFOVWideDeg * UnitsPerDegree
	want := int(hw + 0.5)
	if p != want {
		t.Fatalf("right edge: got %d want %d", p, want)
	}
	// Top of frame tilts up (positive).
	if _, tl := WBTarget(0, 0, 0, 0.5, 0.0); tl <= 0 {
		t.Fatalf("top tap should tilt up, got %d", tl)
	}
	// FOV endpoints.
	if d := HFOVDegrees(ZoomOpticalMax) - HFOVTeleDeg; d > 1e-9 || d < -1e-9 {
		t.Fatal("tele fov")
	}
}

func TestControlFrames(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"preset speed 18", PresetSpeed(18), []byte{0x81, 0x01, 0x06, 0x01, 0x12, 0xFF}},
		{"preset speed clamps", PresetSpeed(99), []byte{0x81, 0x01, 0x06, 0x01, 0x18, 0xFF}},
		{"focus lock", FocusLock(true), []byte{0x81, 0x0A, 0x04, 0x68, 0x02, 0xFF}},
		{"focus unlock", FocusLock(false), []byte{0x81, 0x0A, 0x04, 0x68, 0x03, 0xFF}},
		{"focus far 2", FocusDriveSpeed(1, 2), []byte{0x81, 0x01, 0x04, 0x08, 0x22, 0xFF}},
		{"focus near 7", FocusDriveSpeed(-1, 9), []byte{0x81, 0x01, 0x04, 0x08, 0x37, 0xFF}},
		{"focus stop", FocusDriveSpeed(0, 3), []byte{0x81, 0x01, 0x04, 0x08, 0x00, 0xFF}},
		{"osd toggle", OSDToggle(), []byte{0x81, 0x01, 0x04, 0x3F, 0x02, 0x5F, 0xFF}},
		{"menu off", MenuOff(), []byte{0x81, 0x01, 0x06, 0x06, 0x03, 0xFF}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: % X, want % X", c.name, c.got, c.want)
		}
	}
}
