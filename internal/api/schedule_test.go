package api

import (
	"testing"
	"time"
)

func TestScheduledActions(t *testing.T) {
	e := setup(t)
	code, r, _ := e.do(t, "POST", "/api/schedule", map[string]any{"inSeconds": 1, "by": "Booth",
		"steps": []any{map[string]any{"type": "slides", "action": "next"}}})
	if code != 201 || r["status"] != "pending" || r["label"] != "Slides: next" || r["by"] != "Booth" {
		t.Fatalf("%d %v", code, r)
	}
	id := r["id"].(string)
	if code, r, _ := e.do(t, "POST", "/api/schedule", map[string]any{"inSeconds": 5, "steps": []any{map[string]any{"type": "dance"}}}); code != 422 {
		t.Fatalf("bad step accepted: %d %v", code, r)
	}
	later, _, _ := func() (string, int, error) {
		_, r, _ := e.do(t, "POST", "/api/schedule", map[string]any{"inSeconds": 600, "label": "Cut later",
			"steps": []any{map[string]any{"type": "switcher", "action": "cut"}}})
		return r["id"].(string), 0, nil
	}()
	if code, _, _ := e.do(t, "DELETE", "/api/schedule/"+later, nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, r, _ = e.do(t, "GET", "/api/schedule", nil)
		var st string
		for _, j := range r["jobs"].([]any) {
			if jm := j.(map[string]any); jm["id"] == id {
				st, _ = jm["status"].(string)
			}
		}
		if st == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not run: %v", r)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
