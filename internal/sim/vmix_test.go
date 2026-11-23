package sim

import (
	"bufio"
	"image"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func serviceSwitcher(t *testing.T) *Switcher {
	t.Helper()
	f := func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, W, H)) }
	s := NewSwitcher([]Input{{"Wide", "Capture", f}, {"Lectern", "Capture", f}, {"Slides", "PowerPoint", f}})
	s.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	s.AddServiceInputs()
	return s
}

func run(t *testing.T, s *Switcher, query string) {
	t.Helper()
	q, _ := url.ParseQuery(query)
	if err := s.function(q); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestSwitcherFullXML(t *testing.T) {
	s := serviceSwitcher(t)
	x := string(s.stateXML())
	for _, want := range []string{
		`<version>29.0.0.50</version>`,
		`title="Lower third"`, `<text index="0" name="Name.Text">Guest speaker</text>`,
		`type="VideoList"`, `selectedIndex="1"`, `<item selected="true">`,
		`<transition number="1" effect="Fade" duration="500" />`,
		`<overlay number="1" />`, `<recording duration="`, `<streaming channel1="True"`,
		`<busA name="Stream"`, `<busG `, `headphonesVolume=`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("xml lacks %s", want)
		}
	}
	// the stage mix is live and its meters move
	// (polled for up to 2 s: a slow CI runner can read the same value twice
	// in a row, which once failed this on Windows)
	m1 := xmlAttr(t, x, "Stage mix", "meterF1")
	m2 := m1
	for deadline := time.Now().Add(2 * time.Second); m2 == m1 && time.Now().Before(deadline); {
		time.Sleep(60 * time.Millisecond)
		m2 = xmlAttr(t, string(s.stateXML()), "Stage mix", "meterF1")
	}
	if m1 == 0 || m1 == m2 {
		t.Fatalf("stage mix meter should move: %v %v", m1, m2)
	}
	// a paused clip is silent until played; then its position runs
	if xmlAttr(t, x, "Countdown", "meterF1") != 0 {
		t.Fatal("paused clip should be silent")
	}
	run(t, s, "Function=Play&Input=Countdown")
	time.Sleep(150 * time.Millisecond)
	x = string(s.stateXML())
	if p := xmlAttr(t, x, "Countdown", "position"); p < 100 {
		t.Fatalf("position should run: %v", p)
	}
	if v, ok := xmlText(x, "vmix/inputs/input[@title='Countdown']/@state"); !ok || v != "Running" {
		t.Fatalf("state: %q", v)
	}
}

func xmlAttr(t *testing.T, x, title, attr string) float64 {
	t.Helper()
	v, ok := xmlText(x, "vmix/inputs/input[@title='"+title+"']/@"+attr)
	if !ok {
		t.Fatalf("no %s on %s", attr, title)
	}
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

func TestSwitcherFunctions(t *testing.T) {
	s := serviceSwitcher(t)
	run(t, s, "Function=OverlayInput1In&Input=Lower third")
	run(t, s, "Function=SetText&Input=Lower third&SelectedName=Name.Text&Value=Pat Doe")
	run(t, s, "Function=SetBusAVolume&Value=55")
	run(t, s, "Function=BusXAudioOff&Value=B")
	run(t, s, "Function=AudioOff&Input=Stage mix")
	run(t, s, "Function=NextItem&Input=Walk-in music")
	run(t, s, "Function=StopRecording")
	x := string(s.stateXML())
	for path, want := range map[string]string{
		"vmix/overlays/overlay[1]":                                 "5",
		"vmix/inputs/input[5]/text[1]":                             "Pat Doe",
		"vmix/audio/busA/@volume":                                  "55.000",
		"vmix/audio/busB/@muted":                                   "True",
		"vmix/inputs/input[@title='Stage mix']/@muted":             "True",
		"vmix/inputs/input[@title='Walk-in music']/@selectedIndex": "2",
		"vmix/recording":                                           "False",
	} {
		if v, ok := xmlText(x, path); !ok || v != want {
			t.Errorf("%s = %q, want %q", path, v, want)
		}
	}
	// the T-bar: all the way takes the preview
	if s.Tally(1) != 1 || s.Tally(2) != 2 {
		t.Fatal("start: 1 live, 2 preview")
	}
	run(t, s, "Function=SetFader&Value=128")
	if s.Tally(1) != 1 {
		t.Fatal("half-way is not a take")
	}
	run(t, s, "Function=SetFader&Value=255")
	if s.Tally(2) != 1 || s.Tally(1) != 2 {
		t.Fatal("T-bar at the end takes the preview")
	}
	// the program picture carries the lower third
	dst := image.NewRGBA(image.Rect(0, 0, W, H))
	s.Program(dst)
	if c := dst.RGBAAt(86, H-120); c.R < 200 || c.G < 120 {
		t.Fatalf("lower third accent missing: %v", c)
	}
	q, _ := url.ParseQuery("Function=NoSuchThing")
	if s.function(q) != errNoFunction {
		t.Fatal("unknown function")
	}
}

func TestSwitcherTCP(t *testing.T) {
	s := serviceSwitcher(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if err := s.ServeTCP(addr); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c)
	line := func() string {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimRight(l, "\r\n")
	}
	send := func(s string) { c.Write([]byte(s + "\r\n")) }
	if l := line(); l != "VERSION OK 29.0.0.50" {
		t.Fatalf("greeting: %q", l)
	}
	send("SUBSCRIBE TALLY")
	if l := line(); l != "SUBSCRIBE OK TALLY Subscribed" {
		t.Fatal(l)
	}
	if l := line(); !strings.HasPrefix(l, "TALLY OK 12000") {
		t.Fatal(l)
	}
	send("SUBSCRIBE ACTS")
	line()
	send("FUNCTION PreviewInput Input=3")
	var got []string
	for len(got) < 6 {
		got = append(got, line())
		if got[len(got)-1] == "FUNCTION OK Completed" {
			break
		}
	}
	send("FUNCTION Cut")
	deadline := time.Now().Add(2 * time.Second)
	sawTally := false
	for !sawTally && time.Now().Before(deadline) {
		l := line()
		sawTally = strings.HasPrefix(l, "TALLY OK 201")
	}
	if !sawTally {
		t.Fatal("no tally push after Cut")
	}
	send("XMLTEXT vmix/active")
	for {
		l := line()
		if strings.HasPrefix(l, "XMLTEXT") {
			if l != "XMLTEXT OK 3" {
				t.Fatal(l)
			}
			break
		}
	}
	send("PING")
	for line() != "PING OK PONG" {
	}
	send("XML")
	for {
		l := line()
		if strings.HasPrefix(l, "XML ") {
			n, _ := strconv.Atoi(strings.TrimPrefix(l, "XML "))
			body := make([]byte, n)
			if _, err := io.ReadFull(br, body); err != nil || !strings.HasPrefix(string(body), "<vmix>") {
				t.Fatalf("xml body: %v", err)
			}
			break
		}
	}
}
