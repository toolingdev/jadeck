package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDiagnostics(t *testing.T) {
	e := setup(t)

	// Something fails: the reason lands in the log.
	e.do(t, "POST", "/api/stage/channels/nope/messages", map[string]any{"text": "hi"})
	e.do(t, "POST", "/api/stage/channels/pulpit/messages", map[string]any{"text": "Wrap up", "from": "Booth"})

	_, snap, _ := e.do(t, "GET", "/api/diag", nil)
	for _, k := range []string{"version", "uptime", "cameras", "health", "devices", "system", "stage", "problems", "logFiles"} {
		if _, ok := snap[k]; !ok {
			t.Errorf("snapshot missing %q", k)
		}
	}
	if cams := snap["cameras"].([]any); len(cams) != 1 || cams[0].(map[string]any)["connected"] != true {
		t.Fatalf("cameras: %v", snap["cameras"])
	}
	problems := snap["problems"].([]any)
	found := false
	for _, p := range problems {
		a, _ := p.(map[string]any)["attrs"].(map[string]any)
		if a["path"] == "/api/stage/channels/nope/messages" && strings.Contains(a["reason"].(string), "channel") {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed request not logged with its reason: %v", problems)
	}

	// Text search over the log.
	_, l, _ := e.do(t, "GET", "/api/diag/logs?q=wrap+up", nil)
	if n := len(l["entries"].([]any)); n != 1 {
		t.Fatalf("log search found %d", n)
	}

	// A browser error.
	if code, _, _ := e.do(t, "POST", "/api/diag/client", map[string]any{"msg": "TypeError: x is undefined", "page": "/dashboard", "screen": "Pulpit iPad"}); code != 204 {
		t.Fatalf("client log: %d", code)
	}
	_, l, _ = e.do(t, "GET", "/api/diag/logs?level=error&q=TypeError", nil)
	if len(l["entries"].([]any)) != 1 {
		t.Fatalf("browser error not logged: %v", l)
	}

	// Level change.
	if code, b, _ := e.do(t, "POST", "/api/diag/level", map[string]any{"level": "debug"}); code != 200 || b["level"] != "debug" {
		t.Fatalf("level: %d %v", code, b)
	}

	// Problem report: saved, listed, readable.
	code, rep, _ := e.do(t, "POST", "/api/diag/report", map[string]any{"note": "tally not lighting", "screen": "Pulpit iPad"})
	if code != 201 {
		t.Fatalf("report: %d %v", code, rep)
	}
	_, _, list := e.do(t, "GET", "/api/diag/reports", nil)
	if len(list) != 1 || list[0].(map[string]any)["note"] != "tally not lighting" {
		t.Fatalf("reports: %v", list)
	}
	_, full, _ := e.do(t, "GET", "/api/diag/reports/"+rep["name"].(string), nil)
	if full["snapshot"] == nil || len(full["log"].([]any)) == 0 {
		t.Fatalf("report body: %v", full)
	}
	if code, _, _ := e.do(t, "GET", "/api/diag/reports/..%2Fsettings.json", nil); code != 404 {
		t.Fatalf("path escape: %d", code)
	}

	// The log file on disk.
	resp, err := http.Get(e.url + "/api/diag/logs/file?tail=50")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "stage message") {
		t.Fatalf("log file: %d %s", resp.StatusCode, body)
	}
	if code, _, _ := e.do(t, "GET", "/api/diag/logs/file?name=../settings.json", nil); code != 404 {
		t.Fatalf("file escape: %d", code)
	}
}

func TestDiagIsTrustedOnly(t *testing.T) {
	for _, p := range []string{"/api/diag", "/api/diag/logs", "/api/diag/report", "/api/diag/reports/x"} {
		if got := scopeFor("GET", p); got != scopeTrusted {
			t.Errorf("%s = %q", p, got)
		}
	}
}
