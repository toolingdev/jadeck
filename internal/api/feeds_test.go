package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"ptz/internal/devices"
	"ptz/internal/settings"
	"ptz/internal/video"
)

// as sends a request with a bearer token (from the trusted network: the
// token still decides what is allowed).
func (e *env) as(t *testing.T, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func js(v any) string { return string(mustJSON(v)) }

func writeFile(path, s string) error { return os.WriteFile(path, []byte(s), 0o600) }

func feedByID(r map[string]any, id string) map[string]any {
	l, _ := r["feeds"].([]any)
	for _, x := range l {
		if f := x.(map[string]any); f["id"] == id {
			return f
		}
	}
	return nil
}

func TestFeedsScopeMap(t *testing.T) {
	for route, want := range map[string]string{
		"GET /api/feeds":                scopeAny,
		"POST /api/feeds":               "feeds",
		"PUT /api/feeds/program":        "feeds",
		"DELETE /api/feeds/program":     "feeds",
		"POST /api/feeds/program/key":   "feeds",
		"GET /api/video":                scopeTrusted,
		"POST /api/connections/test":    scopeTrusted,
		"POST /api/connections/x/state": "addons",
	} {
		m, p, _ := strings.Cut(route, " ")
		if got := scopeFor(m, p); got != want {
			t.Errorf("%s: %q want %q", route, got, want)
		}
	}
}

func TestFeedsPermission(t *testing.T) {
	e := setup(t)
	// The trusted network adds a push feed: it gets a publish key at once.
	code, r, _ := e.do(t, "POST", "/api/feeds", map[string]any{"name": "Program", "source": "push", "key": "chosen-by-me", "owner": "x"})
	if code != 200 || r["id"] != "program" {
		t.Fatalf("add feed: %d %v", code, r)
	}
	key, _ := r["key"].(string)
	if len(key) != 32 || key == "chosen-by-me" || r["owner"] != nil {
		t.Fatalf("a caller can't pick the key or owner: %v", r)
	}
	push := r["push"].(map[string]any)
	if !strings.HasSuffix(push["url"].(string), "?streamid=publish:program:program:"+key) ||
		!strings.HasPrefix(push["rtspUrl"].(string), "rtsp://program:"+key+"@") {
		t.Fatalf("sender addresses: %v", push)
	}
	// The gateway config takes the stream only with that key.
	if conf := video.Config(e.srv.Settings.Get()); !strings.Contains(conf, "action: publish\n    path: 'program'") {
		t.Fatalf("gateway config: %s", conf)
	}

	// Keys: without the feeds permission, unchanged (refused).
	_, kr, _ := e.do(t, "POST", "/api/keys", map[string]any{"name": "viewer", "scopes": []string{"camera.read"}})
	viewer := kr["secret"].(string)
	_, kr, _ = e.do(t, "POST", "/api/keys", map[string]any{"name": "feeds bot", "scopes": []string{"feeds"}})
	bot := kr["secret"].(string)
	if c, _ := e.as(t, viewer, "POST", "/api/feeds", map[string]any{"name": "Lobby", "source": "push"}); c != 403 {
		t.Fatalf("key without feeds may add a feed: %d", c)
	}
	if c, _ := e.as(t, viewer, "DELETE", "/api/feeds/program", nil); c != 403 {
		t.Fatalf("key without feeds may remove a feed: %d", c)
	}
	// ...and the list shows it no key.
	_, l := e.as(t, viewer, "GET", "/api/feeds", nil)
	if f := feedByID(l, "program"); f == nil || f["push"].(map[string]any)["key"] != nil || f["push"].(map[string]any)["url"] != nil ||
		f["canEdit"] != false || strings.Contains(js(l), key) {
		t.Fatalf("a key without feeds sees the publish key: %v", f)
	}
	if c := e.asOutsider(t, "POST", "/api/feeds", ""); c != 401 {
		t.Fatalf("outsider without a key: %d", c)
	}

	// An API key with feeds manages every feed and sees keys.
	if c, r := e.as(t, bot, "POST", "/api/feeds", map[string]any{"name": "Lobby", "source": "push"}); c != 200 || r["key"] == nil {
		t.Fatalf("feeds key adds a feed: %d %v", c, r)
	}
	_, l = e.as(t, bot, "GET", "/api/feeds", nil)
	if f := feedByID(l, "program"); f["push"].(map[string]any)["key"] != key || f["canEdit"] != true {
		t.Fatalf("feeds key list: %v", f)
	}
	if c, r := e.as(t, bot, "PUT", "/api/feeds/program", map[string]any{"name": "Program out", "source": "push"}); c != 200 || r["key"] != key {
		t.Fatalf("renaming keeps the key: %d %v", c, r)
	}

	// An add-on connection with feeds: owns what it adds, nothing else.
	addon := e.srv.GrantAddon(devices.Connection{ID: "conn-bridge", Name: "Bridge"}, []string{"feeds"}).Token
	other := e.srv.GrantAddon(devices.Connection{ID: "conn-other", Name: "Other"}, []string{"feeds"}).Token
	noFeeds := e.srv.GrantAddon(devices.Connection{ID: "conn-ai", Name: "AI"}, []string{"camera.read"}).Token
	if c, _ := e.as(t, noFeeds, "POST", "/api/feeds", map[string]any{"name": "AI", "source": "push"}); c != 403 {
		t.Fatalf("add-on without feeds adds a feed: %d", c)
	}
	c, r := e.as(t, addon, "POST", "/api/feeds", map[string]any{"name": "Bridge 1", "source": "push"})
	if c != 200 || r["owner"] != "conn-bridge" || r["key"] == nil {
		t.Fatalf("add-on adds a feed: %d %v", c, r)
	}
	bkey := r["key"].(string)
	if f, _ := e.srv.Settings.Get().Video.Feed("bridge-1"); f.Owner != "conn-bridge" {
		t.Fatalf("owner not recorded: %+v", f)
	}
	for _, try := range []struct{ method, path string }{
		{"PUT", "/api/feeds/program"}, {"DELETE", "/api/feeds/program"}, {"POST", "/api/feeds/program/key"},
	} {
		if c, _ := e.as(t, addon, try.method, try.path, map[string]any{"name": "Mine now", "source": "push"}); c != 403 {
			t.Errorf("add-on %s %s on a feed it didn't add: %d", try.method, try.path, c)
		}
	}
	if c, _ := e.as(t, other, "DELETE", "/api/feeds/bridge-1", nil); c != 403 {
		t.Fatalf("another add-on removes the feed: %d", c)
	}
	_, l = e.as(t, addon, "GET", "/api/feeds", nil)
	if feedByID(l, "bridge-1")["push"].(map[string]any)["key"] != bkey || feedByID(l, "program")["push"].(map[string]any)["key"] != nil {
		t.Fatalf("add-on sees keys of its own feeds only: %v", l)
	}
	c, r = e.as(t, addon, "POST", "/api/feeds/bridge-1/key", nil)
	if c != 200 || r["key"] == bkey || r["key"] == nil {
		t.Fatalf("new key: %d %v", c, r)
	}
	if c, _ := e.as(t, addon, "DELETE", "/api/feeds/bridge-1", nil); c != 204 {
		t.Fatalf("add-on removes its own feed: %d", c)
	}

	// Token changes are in the audit log; keys never are.
	_, _, audit := e.do(t, "GET", "/api/audit", nil)
	all := js(audit)
	for _, want := range []string{"video feed added: bridge-1 (push)", "video feed bridge-1: new publish key", "video feed removed: bridge-1",
		"video feed added: lobby (push)", "Bridge (add-on)", "denied: needs feeds"} {
		if !strings.Contains(all, want) {
			t.Errorf("audit lacks %q", want)
		}
	}
	if strings.Contains(all, key) || strings.Contains(all, bkey) {
		t.Error("a publish key is in the audit log")
	}
}

func TestFeedKeysMigrate(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.json"
	old := `{"video":{"managed":true,"feeds":[{"id":"program","name":"Program","source":"push"},{"id":"lobby","name":"Lobby","source":"pull","url":"rtsp://x/1"}]}}`
	if err := writeFile(path, old); err != nil {
		t.Fatal(err)
	}
	st, err := settings.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.Get().Video.Feed("program")
	l, _ := st.Get().Video.Feed("lobby")
	if len(p.Key) != 32 || l.Key != "" {
		t.Fatalf("after upgrade: %+v %+v", p, l)
	}
	again, _ := settings.Open(path, nil)
	if p2, _ := again.Get().Video.Feed("program"); p2.Key != p.Key {
		t.Fatal("the generated key was not saved")
	}
	// legacy program/confidence switches become keyed feeds too
	st.Update(func(s *settings.Settings) error {
		s.Video.Feeds = nil
		s.Video.Confidence = true
		s.Video.MigrateFeeds()
		return nil
	})
	if c, _ := st.Get().Video.Feed("confidence"); c.Key == "" {
		t.Fatal("migrated confidence feed has no key")
	}
	if strings.Contains(js(st.Get().Video.Redacted()), p.Key) {
		t.Fatal("Redacted shows a key")
	}
}

// A sender the gateway turned away shows on the feed it was meant for.
func TestFeedsLastRefused(t *testing.T) {
	e := setup(t)
	if code, r, _ := e.do(t, "POST", "/api/feeds", map[string]any{"name": "Program", "source": "push"}); code != 200 {
		t.Fatalf("add: %d %v", code, r)
	}
	if code, r, _ := e.do(t, "POST", "/api/feeds", map[string]any{"name": "Lobby", "source": "pull", "url": "rtsp://10.0.0.5/1"}); code != 200 {
		t.Fatalf("add: %d %v", code, r)
	}
	_, l, _ := e.do(t, "GET", "/api/feeds", nil)
	if st, _ := feedByID(l, "program")["status"].(map[string]any); st != nil && st["lastRefused"] != nil {
		t.Fatalf("refused before any sender: %v", st)
	}
	at := time.Now().Add(-time.Minute).Format("2006/01/02 15:04:05")
	e.srv.Video.NoteLogLine(at + " INF [SRT] [conn 192.168.1.50:61000] closed: failed to authenticate: authentication failed")
	_, l, _ = e.do(t, "GET", "/api/feeds", nil)
	st, _ := feedByID(l, "program")["status"].(map[string]any)
	rf, _ := st["lastRefused"].(map[string]any)
	if rf == nil || rf["reason"] != "key" || rf["remote"] != "192.168.1.50" || rf["at"] == nil {
		t.Fatalf("program status: %v", st)
	}
	if st, _ := feedByID(l, "lobby")["status"].(map[string]any); st != nil && st["lastRefused"] != nil {
		t.Fatalf("a pulled feed shows a refused sender: %v", st)
	}
}

// /api/config tells the Video widget's header where a feed comes from: a
// pull feed's protocol, never its URL (it can carry a password).
func TestConfigFeedSource(t *testing.T) {
	e := setup(t)
	if code, r, _ := e.do(t, "POST", "/api/feeds", map[string]any{"name": "Lobby", "source": "pull", "url": "srt://user:secret@10.0.0.9:9000"}); code != 200 {
		t.Fatalf("add feed: %d %v", code, r)
	}
	e.do(t, "POST", "/api/feeds", map[string]any{"name": "Program", "source": "push"})
	_, cfg, _ := e.do(t, "GET", "/api/config", nil)
	if strings.Contains(js(cfg), "secret") {
		t.Fatalf("a pull URL leaked into /api/config: %v", cfg)
	}
	got := map[string]map[string]any{}
	for _, f := range cfg["feeds"].([]any) {
		m := f.(map[string]any)
		got[m["id"].(string)] = m
	}
	if got["lobby"]["proto"] != "srt" || got["lobby"]["via"] != nil {
		t.Fatalf("pull feed: %v", got["lobby"])
	}
	if got["program"]["proto"] != nil || got["program"]["source"] != "push" {
		t.Fatalf("push feed: %v", got["program"])
	}
}
