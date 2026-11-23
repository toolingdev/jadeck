package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ptz/internal/dashboards"
	"ptz/internal/remote"
	"ptz/internal/remote/remotetest"
)

type remoteEnv struct {
	*env
	door  *httptest.Server
	keys  *remotetest.Keys
	mine  int64 // the pastor's dashboard
	other int64
}

func setupRemote(t *testing.T) *remoteEnv {
	e := setup(t)
	ks := remotetest.New(t)
	st, err := remote.Open(filepath.Join(t.TempDir(), "remote.json"))
	if err != nil {
		t.Fatal(err)
	}
	e.srv.Remote, e.srv.RemoteVerify = st, ks.Verifier()
	mine, err := e.srv.Dashboards.Create(&dashboards.Dashboard{Name: "Pastor", Kind: "phone", Widgets: []dashboards.Widget{
		{ID: "live", Type: "stage.live", Settings: map[string]any{"mode": "stream"}},
		{ID: "msg", Type: "stage.messages", Settings: map[string]any{"channels": []any{"pastor"}}},
		{ID: "chat", Type: "addon:chat:live_chat", Settings: map[string]any{"addon": "chat", "connection": "live-chat"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := e.srv.Dashboards.Create(&dashboards.Dashboard{Name: "Booth", Kind: "desktop"})
	if err := st.Set(remote.Config{Enabled: true, Team: remotetest.Team, AUD: remotetest.AUD, People: []remote.Person{
		{Email: "pastor@example.com", Name: "Pastor", Dashboards: []int64{mine.ID}},
		{Email: "me@example.com", Name: "Me", Everything: true},
	}}); err != nil {
		t.Fatal(err)
	}
	door := httptest.NewServer(e.srv.RemoteHandler())
	t.Cleanup(door.Close)
	return &remoteEnv{env: e, door: door, keys: ks, mine: mine.ID, other: other.ID}
}

// as sends a request over the remote door signed in as email ("" = no sign-in).
func (r *remoteEnv) as(t *testing.T, email, method, path, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, r.door.URL+path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if email != "" {
		req.Header.Set("Cf-Access-Jwt-Assertion", r.keys.Token(email))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestRemoteDoor(t *testing.T) {
	r := setupRemote(t)
	id := func(n int64) string { return strconv.FormatInt(n, 10) }

	// no sign-in, or someone not on the list
	if code, _ := r.as(t, "", "GET", "/api/dashboards", ""); code != 401 {
		t.Fatalf("no sign-in: %d", code)
	}
	if code, body := r.as(t, "stranger@example.com", "GET", "/api/dashboards", ""); code != 403 || !strings.Contains(body, "hasn't let stranger@example.com in") {
		t.Fatalf("stranger: %d %s", code, body)
	}

	// the pastor: only his dashboard
	code, body := r.as(t, "pastor@example.com", "GET", "/api/dashboards", "")
	var l []dashboards.Dashboard
	json.Unmarshal([]byte(body), &l)
	if code != 200 || len(l) != 1 || l[0].ID != r.mine {
		t.Fatalf("pastor's list: %d %s", code, body)
	}
	if code, _ := r.as(t, "pastor@example.com", "GET", "/api/dashboards/"+id(r.mine), ""); code != 200 {
		t.Fatalf("own dashboard: %d", code)
	}
	if code, _ := r.as(t, "pastor@example.com", "GET", "/api/dashboards/"+id(r.other), ""); code != 403 {
		t.Fatalf("other dashboard: %d", code)
	}
	code, body = r.as(t, "pastor@example.com", "GET", "/api/remote/me", "")
	if code != 200 || !strings.Contains(body, `"remote":true`) || !strings.Contains(body, `"everything":false`) {
		t.Fatalf("me: %d %s", code, body)
	}
	// what his screen needs
	for _, p := range []string{"/", "/dashboard", "/api/stage", "/api/switcher/status", "/api/addons", "/api/kit/v1/kit.js"} {
		if code, body := r.as(t, "pastor@example.com", "GET", p, ""); code == 403 {
			t.Errorf("GET %s refused: %s", p, body)
		}
	}
	// messages on his channel only, never clearing or other channels
	if code, body := r.as(t, "pastor@example.com", "POST", "/api/stage/channels/pastor/messages", `{"text":"2 more min"}`); code == 403 {
		t.Errorf("message on his channel refused: %s", body)
	}
	for _, c := range []struct{ m, p, b string }{
		{"POST", "/api/stage/channels/pulpit/messages", `{"text":"x"}`},
		{"DELETE", "/api/stage/channels/pastor/messages", ""},
		{"POST", "/api/stage/channels/pastor/timer", `{"op":"stop"}`},
		{"POST", "/api/connections/live-chat/actions/reply", `{"text":"hi"}`},
		{"POST", "/api/ptz/home", ""},
		{"POST", "/api/switcher/cut", ""},
		{"GET", "/api/connections", ""},
		{"GET", "/api/system", ""},
		{"GET", "/api/keys", ""},
		{"PUT", "/api/remote", `{}`},
		{"GET", "/api/connections/other/state", ""},
	} {
		if code, _ := r.as(t, "pastor@example.com", c.m, c.p, c.b); code != 403 {
			t.Errorf("%s %s: %d, want 403", c.m, c.p, code)
		}
	}
	// marking a comment addressed on his chat is the one add-on action he has
	if code, body := r.as(t, "pastor@example.com", "POST", "/api/connections/live-chat/actions/comment_addressed", `{"id":"x"}`); code == 403 && strings.Contains(body, "not on your screen") {
		t.Errorf("comment_addressed refused: %s", body)
	}
	if code, _ := r.as(t, "pastor@example.com", "GET", "/api/connections/live-chat/state", ""); code == 403 {
		t.Error("his chat's state refused")
	}

	// "everything": the whole app, but never remote access or keys from outside
	if code, _ := r.as(t, "me@example.com", "GET", "/api/system", ""); code != 200 {
		t.Errorf("everything: /api/system %d", code)
	}
	if code, _ := r.as(t, "me@example.com", "GET", "/api/dashboards/"+id(r.other), ""); code != 200 {
		t.Errorf("everything: other dashboard %d", code)
	}
	for _, p := range []string{"/api/remote", "/api/keys"} {
		if code, _ := r.as(t, "me@example.com", "GET", p, ""); code != 403 {
			t.Errorf("everything from outside: GET %s %d", p, code)
		}
	}

	// the local door is unchanged: no sign-in, full access, not "remote"
	if code, m, _ := r.do(t, "GET", "/api/remote/me", nil); code != 200 || m["remote"] != false {
		t.Fatalf("local me: %d %v", code, m)
	}
	if code, _, _ := r.do(t, "GET", "/api/remote", nil); code != 200 {
		t.Fatalf("local settings: %d", code)
	}

	// turned off: the door refuses everyone
	c := r.srv.Remote.Get()
	c.Enabled = false
	r.srv.Remote.Set(c)
	if code, _ := r.as(t, "me@example.com", "GET", "/api/dashboards", ""); code != 503 {
		t.Fatalf("off: %d", code)
	}
}

func TestRemoteSettings(t *testing.T) {
	r := setupRemote(t)
	var applied *remote.Config
	r.srv.OnRemote = func(c remote.Config) error { applied = &c; return nil }
	code, m, _ := r.do(t, "PUT", "/api/remote", map[string]any{"enabled": true, "team": "church", "aud": remotetest.AUD, "link": "https://live.example.com",
		"people": []any{map[string]any{"email": "Pastor@Example.com", "name": "Pastor", "dashboards": []int64{r.mine}}}})
	if code != 200 || applied == nil || !applied.Enabled || applied.People[0].Email != "pastor@example.com" {
		t.Fatalf("%d %v", code, m)
	}
	if code, m, _ := r.do(t, "PUT", "/api/remote", map[string]any{"enabled": true}); code != 400 || !strings.Contains(m["detail"].(string), "team") {
		t.Fatalf("%d %v", code, m)
	}
}
