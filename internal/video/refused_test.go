package video

import (
	"testing"
	"time"
)

func TestParseRefusal(t *testing.T) {
	for line, want := range map[string]Refusal{
		// MediaMTX v1.21.4's own lines (RTSP seen live; SRT from its source)
		"2026/11/21 10:42:07 INF [SRT] [conn 192.168.1.50:61000] closed: failed to authenticate: authentication failed": {Reason: "key", Remote: "192.168.1.50", Proto: "SRT"},
		"2026/11/21 10:42:07 INF [RTSP] [conn 127.0.0.1:55162] closed: failed to authenticate: authentication failed":   {Reason: "key", Remote: "127.0.0.1", Proto: "RTSP"},
		"2026/11/21 10:42:07 INF [RTSP] [conn 127.0.0.1:55168] closed: path 'confidence' is not configured":             {Reason: "path", Path: "confidence", Remote: "127.0.0.1", Proto: "RTSP"},
		"2026/11/21 10:42:07 INF [SRT] [conn [fe80::1]:5000] closed: invalid stream ID 'x': bad":                        {Reason: "streamid", Remote: "fe80::1", Proto: "SRT"},
	} {
		got, ok := parseRefusal(line)
		if !ok {
			t.Errorf("not a refusal: %s", line)
			continue
		}
		at := time.Date(2026, 11, 21, 10, 42, 7, 0, time.Local)
		if !got.At.Equal(at) {
			t.Errorf("%s: at %v", line, got.At)
		}
		got.At = time.Time{}
		if got != want {
			t.Errorf("%s:\n got %+v\nwant %+v", line, got, want)
		}
	}
	for _, line := range []string{
		"2026/11/21 10:42:07 WAR [RTSP] [session b6c7e7d6] failed to authenticate: authentication failed", // same refusal, counted once (conn line)
		"2026/11/21 10:42:07 INF [RTSP] [conn 127.0.0.1:55169] closed: EOF",
		"2026/11/21 10:42:07 INF [RTSP] [conn 127.0.0.1:55169] closed: path 'cam-main-sd' is not configured",
		"2026/11/21 10:42:07 INF [SRT] [conn 1.2.3.4:5] opened",
		"2026/11/21 10:42:07 INF reloading configuration (file changed)",
	} {
		if r, ok := parseRefusal(line); ok {
			t.Errorf("taken as a refusal: %s → %+v", line, r)
		}
	}
}

func TestRefusedPerFeed(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 50, 0, 0, time.Local)
	nowForRefuse = func() time.Time { return now }
	defer func() { nowForRefuse = time.Now }()
	g := &Gateway{}
	g.noteSenders([]SenderStat{{Path: "program", Remote: "192.168.1.50:61000"}}, now.Add(-time.Hour))
	line := func(at, rest string) { g.NoteLogLine("2026/11/21 " + at + " INF " + rest) }

	line("10:30:00", "[SRT] [conn 192.168.1.77:1] closed: failed to authenticate: authentication failed") // too old
	line("10:42:00", "[SRT] [conn 192.168.1.50:61000] closed: failed to authenticate: authentication failed")
	push := []string{"program", "confidence", "slides"}
	got := g.Refused(push, map[string]bool{"slides": true})
	if r, ok := got["program"]; !ok || r.Reason != "key" || r.Remote != "192.168.1.50" {
		t.Fatalf("program (the address that sent it before): %+v", got)
	}
	if _, ok := got["confidence"]; ok {
		t.Fatalf("a known sender's refusal shown on another feed: %+v", got)
	}

	// a new address with a wrong key: shown on every feed still waiting
	line("10:44:00", "[SRT] [conn 192.168.1.90:2] closed: failed to authenticate: authentication failed")
	got = g.Refused(push, map[string]bool{"slides": true})
	if got["confidence"].Remote != "192.168.1.90" || got["program"].Remote != "192.168.1.90" {
		t.Fatalf("new sender: %+v", got)
	}
	if _, ok := got["slides"]; ok {
		t.Fatal("shown on a feed that is receiving")
	}

	// a path that should exist (missed reload) names its feed
	line("10:45:00", "[SRT] [conn 192.168.1.91:3] closed: path 'confidence' is not configured")
	got = g.Refused(push, nil)
	if r := got["confidence"]; r.Reason != "path" || r.Path != "confidence" {
		t.Fatalf("path refusal: %+v", r)
	}
	if got["program"].Remote != "192.168.1.90" {
		t.Fatalf("named path shown on another feed: %+v", got["program"])
	}
	// a path that isn't a feed here (old Stream ID): on the waiting feeds
	line("10:46:00", "[SRT] [conn 192.168.1.92:3] closed: path 'progam' is not configured")
	got = g.Refused(push, map[string]bool{"confidence": true})
	if r := got["program"]; r.Path != "progam" {
		t.Fatalf("unknown path: %+v", r)
	}
	// and it all ages out
	now = now.Add(RefusedWindow + time.Minute)
	if got := g.Refused(push, nil); len(got) != 0 {
		t.Fatalf("old refusals still shown: %+v", got)
	}
}
