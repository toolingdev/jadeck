package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStageMessagesAndEvents(t *testing.T) {
	e := setup(t)

	// Defaults come ready.
	_, st, _ := e.do(t, "GET", "/api/stage", nil)
	chans := st["channels"].([]any)
	if len(chans) != 2 || chans[0].(map[string]any)["id"] != "pulpit" {
		t.Fatalf("default channels: %v", st)
	}

	// The pulpit iPad listens.
	req, _ := http.NewRequest("GET", e.url+"/api/stage/events?screen=Pulpit+iPad&channels=pulpit", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	events := make(chan map[string]any, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if l := sc.Text(); strings.HasPrefix(l, "data: ") {
				var m map[string]any
				json.Unmarshal([]byte(l[6:]), &m)
				events <- m
			}
		}
	}()
	next := func() map[string]any {
		select {
		case m := <-events:
			return m
		case <-time.After(3 * time.Second):
			t.Fatal("no event")
			return nil
		}
	}
	first := next()["channels"].([]any)
	if len(first) != 1 {
		t.Fatalf("subscribed to pulpit only, got %d channels", len(first))
	}
	if sc := first[0].(map[string]any)["screens"].([]any); len(sc) != 1 || sc[0].(map[string]any)["name"] != "Pulpit iPad" {
		t.Fatalf("presence: %v", sc)
	}

	// Booth sends; the iPad gets it.
	code, m, _ := e.do(t, "POST", "/api/stage/channels/pulpit/messages", map[string]any{"text": "  Please wrap up — 5 minutes ", "from": "Booth iPad"})
	if code != 201 || m["text"] != "Please wrap up — 5 minutes" {
		t.Fatalf("send: %d %v", code, m)
	}
	var got []any
	for got == nil || len(got) == 0 {
		got = next()["channels"].([]any)[0].(map[string]any)["messages"].([]any)
	}
	if got[0].(map[string]any)["from"] != "Booth iPad" {
		t.Fatalf("message: %v", got)
	}

	// Seen comes back.
	if code, _, _ := e.do(t, "POST", "/api/stage/channels/pulpit/seen", map[string]any{"id": m["id"], "screen": "Pulpit iPad"}); code != 204 {
		t.Fatalf("seen: %d", code)
	}
	_, st, _ = e.do(t, "GET", "/api/stage", nil)
	msg := st["channels"].([]any)[0].(map[string]any)["messages"].([]any)[0].(map[string]any)
	if seen := msg["seen"].([]any); len(seen) != 1 || seen[0] != "Pulpit iPad" {
		t.Fatalf("seen list: %v", msg)
	}

	// Limits and errors say why.
	if code, b, _ := e.do(t, "POST", "/api/stage/channels/pulpit/messages", map[string]any{"text": strings.Repeat("a", 281)}); code != 422 || !strings.Contains(b["detail"].(string), "280") {
		t.Fatalf("long: %d %v", code, b)
	}
	if code, _, _ := e.do(t, "POST", "/api/stage/channels/nope/messages", map[string]any{"text": "hi"}); code != 404 {
		t.Fatalf("missing channel: %d", code)
	}

	// Timer: start, add, pause.
	code, tm, _ := e.do(t, "POST", "/api/stage/channels/pulpit/timer", map[string]any{"op": "start", "label": "Sermon", "seconds": 1800})
	if code != 200 || tm["timer"].(map[string]any)["running"] != true {
		t.Fatalf("timer start: %d %v", code, tm)
	}
	e.do(t, "POST", "/api/stage/channels/pulpit/timer", map[string]any{"op": "add", "seconds": 120})
	_, tm, _ = e.do(t, "POST", "/api/stage/channels/pulpit/timer", map[string]any{"op": "pause"})
	if left := tm["timer"].(map[string]any)["left"].(float64); left < 1915 || left > 1920 {
		t.Fatalf("paused left %v", left)
	}

	// Channels: create, duplicate name refused, rename, delete.
	code, c, _ := e.do(t, "POST", "/api/stage/channels", map[string]any{"name": "Kids room"})
	if code != 201 || c["id"] != "kids-room" {
		t.Fatalf("create: %d %v", code, c)
	}
	if code, _, _ := e.do(t, "POST", "/api/stage/channels", map[string]any{"name": "kids ROOM"}); code != 422 {
		t.Fatalf("duplicate name: %d", code)
	}
	if code, _, _ := e.do(t, "PUT", "/api/stage/channels/kids-room", map[string]any{"name": "Nursery", "color": "#4ADE80"}); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/stage/channels/kids-room", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}

	// Clear empties the stage.
	e.do(t, "DELETE", "/api/stage/channels/pulpit/messages", nil)
	_, st, _ = e.do(t, "GET", "/api/stage", nil)
	if n := len(st["channels"].([]any)[0].(map[string]any)["messages"].([]any)); n != 0 {
		t.Fatalf("clear left %d", n)
	}
}

func TestStageScopes(t *testing.T) {
	for _, c := range []struct{ m, p, want string }{
		{"GET", "/api/stage", "stage"},
		{"GET", "/api/stage/events", "stage"},
		{"POST", "/api/stage/channels/pulpit/messages", "stage"},
		{"POST", "/api/stage/channels/pulpit/timer", "stage"},
		{"POST", "/api/stage/channels", scopeTrusted},
		{"PUT", "/api/stage/channels/pulpit", scopeTrusted},
		{"DELETE", "/api/stage/channels/pulpit", scopeTrusted},
	} {
		if got := scopeFor(c.m, c.p); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.m, c.p, got, c.want)
		}
	}
}

func TestStageCueReplies(t *testing.T) {
	e := setup(t)
	code, m, _ := e.do(t, "POST", "/api/stage/channels/worship/messages", map[string]any{"text": "Wrap up", "from": "Booth"})
	if code != 201 {
		t.Fatalf("send: %d %v", code, m)
	}
	id := m["id"].(string)
	reply := func(screen, text string) map[string]any {
		code, r, _ := e.do(t, "POST", "/api/stage/channels/worship/reply", map[string]any{"id": id, "screen": screen, "text": text})
		if code != 200 {
			t.Fatalf("reply: %d %v", code, r)
		}
		return r
	}
	// each screen answers for itself; a second answer replaces the first
	reply("Worship lead", "Need 2 min")
	reply("Keys", "👍 Got it")
	r := reply("Worship lead", "👍 Got it")
	reps := r["replies"].([]any)
	if len(reps) != 2 || r["done"] == true {
		t.Fatalf("two screens, one answer each, not done: %v", r)
	}
	if reps[0].(map[string]any)["text"] != "👍 Got it" || len(r["seen"].([]any)) != 2 {
		t.Fatalf("latest answer kept and counts as seen: %v", r)
	}

	// one answer is enough on this channel: the first reply finishes it
	if code, _, _ := e.do(t, "PUT", "/api/stage/channels/worship", map[string]any{"name": "Worship", "color": "#3b82f6", "oneAnswer": true}); code != 200 {
		t.Fatalf("set oneAnswer: %d", code)
	}
	_, m, _ = e.do(t, "POST", "/api/stage/channels/worship/messages", map[string]any{"text": "Mic check", "from": "Booth"})
	id = m["id"].(string)
	if r := reply("Keys", "OK"); r["done"] != true {
		t.Fatalf("one answer is enough: %v", r)
	}
	if code, _, _ := e.do(t, "POST", "/api/stage/channels/worship/reply", map[string]any{"id": "nope", "screen": "Keys", "text": "x"}); code < 400 {
		t.Fatalf("a missing cue is an error: %d", code)
	}
}
