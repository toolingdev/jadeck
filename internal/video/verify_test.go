package video

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ptz/internal/settings"
)

// fakeConfigAPI is a MediaMTX control API holding a running config: the
// paths and users it has loaded. reload (when set) is called on each
// request and may load a newer config, as MediaMTX's file watcher would.
type fakeConfigAPI struct {
	mu     sync.Mutex
	paths  map[string]map[string]any
	users  []authUser
	srt    bool
	calls  []string
	reload func(f *fakeConfigAPI)
}

// load puts s into the fake as if MediaMTX had read its config file.
func (f *fakeConfigAPI) load(s settings.Settings) {
	f.paths = map[string]map[string]any{}
	for _, p := range pathConfs(s) {
		f.paths[p.Name] = p.body()
	}
	f.users = authUsers(s)
	for i := range f.users {
		if f.users[i].Pass != "" {
			f.users[i].Pass = "<redacted>"
		}
	}
	f.srt = (&Gateway{}).srtWanted(s)
}

func (f *fakeConfigAPI) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.paths {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (f *fakeConfigAPI) hasUser(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.User == name {
			return true
		}
	}
	return false
}

func (f *fakeConfigAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reload != nil {
		f.reload(f)
	}
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	switch {
	case r.Method == "GET" && r.URL.Path == "/v3/config/paths/list":
		var items []map[string]any
		for n, p := range f.paths {
			it := map[string]any{"name": n}
			for k, v := range p {
				it[k] = v
			}
			items = append(items, it)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	case r.Method == "GET" && r.URL.Path == "/v3/config/global/get":
		json.NewEncoder(w).Encode(map[string]any{"srt": f.srt, "authInternalUsers": f.users})
	case r.Method == "PATCH" && r.URL.Path == "/v3/config/global/patch":
		var b struct {
			Users []authUser `json:"authInternalUsers"`
			SRT   *bool      `json:"srt"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &b); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if b.Users != nil {
			f.users = b.Users
		}
		if b.SRT != nil {
			f.srt = *b.SRT
		}
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v3/config/paths/add/"):
		if _, ok := f.paths[name]; ok {
			http.Error(w, `{"error":"path already exists"}`, 400)
			return
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.paths[name] = b
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v3/config/paths/replace/"):
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.paths[name] = b
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v3/config/paths/delete/"):
		if _, ok := f.paths[name]; !ok {
			http.NotFound(w, r)
			return
		}
		delete(f.paths, name)
	default:
		http.NotFound(w, r)
	}
}

func twoFeeds() settings.Settings {
	var s settings.Settings
	s.Video.Managed = true
	s.Video.Feeds = []settings.Feed{
		{ID: "program", Name: "Program", Source: "push", Key: "0123456789abcdef0123456789abcdef"},
		{ID: "confidence", Name: "Confidence", Source: "push", Key: "fedcba9876543210fedcba9876543210"},
	}
	return s
}

// runningGateway is a Gateway that counts as running against fake.
func runningGateway(t *testing.T, fake http.Handler) (*Gateway, context.Context) {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	g := &Gateway{Dir: t.TempDir(), API: strings.TrimPrefix(srv.URL, "http://"), settle: 30 * time.Millisecond}
	g.init()
	g.state, g.cmd = "running", &exec.Cmd{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go g.verifyLoop(ctx)
	return g, ctx
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPathConfsMatchConfig(t *testing.T) {
	s := feedSettings()
	s.Cameras = []settings.Camera{{ID: "main", Host: "10.0.0.9", Video: true, StreamHD: "1", StreamSD: "2"}}
	conf := Config(s)
	for _, p := range pathConfs(s) {
		if !strings.Contains(conf, "  "+p.Name+":\n    source: "+q(p.Source)+"\n") && !strings.Contains(conf, "  "+p.Name+":\n    source: "+p.Source+"\n") {
			t.Errorf("path %s (%s) not written as such:\n%s", p.Name, p.Source, conf)
		}
	}
	if n := len(pathConfs(s)); n != 5 { // cam HD + SD, program, lobby, nokey (its path exists but takes nobody)
		t.Errorf("%d paths: %+v", n, pathConfs(s))
	}
}

func TestCheckDrift(t *testing.T) {
	fake := &fakeConfigAPI{}
	fake.load(twoFeeds())
	srv := httptest.NewServer(fake)
	defer srv.Close()
	g := &Gateway{API: strings.TrimPrefix(srv.URL, "http://")}
	ctx := context.Background()

	d, err := g.check(ctx, twoFeeds())
	if err != nil || !d.empty() {
		t.Fatalf("in sync, got %+v %v", d, err)
	}
	// the second feed's reload was missed: no path, no publisher
	one := twoFeeds()
	one.Video.Feeds = one.Video.Feeds[:1]
	fake.load(one)
	d, _ = g.check(ctx, twoFeeds())
	if strings.Join(d.Missing, ",") != "confidence" || strings.Join(d.Users, ",") != "confidence" || d.SRT || len(d.Extra)+len(d.Stale) != 0 {
		t.Fatalf("drift %+v", d)
	}
	// a removed feed still loaded; a pull feed whose address changed; srt off
	fake.load(twoFeeds())
	want := one
	want.Video.Feeds = append(want.Video.Feeds, settings.Feed{ID: "lobby", Source: "pull", URL: "rtsp://10.0.0.5/2"})
	fake.paths["lobby"] = map[string]any{"source": "rtsp://10.0.0.5/1"}
	fake.srt = false
	d, _ = g.check(ctx, want)
	if strings.Join(d.Extra, ",") != "confidence" || strings.Join(d.Stale, ",") != "lobby" || strings.Join(d.Users, ",") != "confidence" || !d.SRT {
		t.Fatalf("drift %+v", d)
	}
	if !strings.Contains(d.String(), "missing") && !strings.Contains(d.String(), "removed paths still loaded confidence") {
		t.Errorf("drift text: %s", d.String())
	}
	// no gateway: an error, not a drift
	if _, err := (&Gateway{API: "127.0.0.1:1"}).check(ctx, want); err == nil {
		t.Fatal("want an error with no gateway")
	}
}

// The venue bug: two push feeds created ~100 ms apart; MediaMTX's
// watcher took the first write and dropped the second. The rewrite gets
// picked up, so nothing goes through the API.
func TestVerifyRewritesMissedChange(t *testing.T) {
	first := twoFeeds()
	first.Video.Feeds = first.Video.Feeds[:1]
	fake := &fakeConfigAPI{}
	fake.load(first)
	var lastMod time.Time
	var g *Gateway
	fake.reload = func(f *fakeConfigAPI) {
		st, err := os.Stat(g.configPath())
		if err != nil {
			return
		}
		if lastMod.IsZero() {
			lastMod = st.ModTime() // this write fell inside the watcher's window
			return
		}
		if st.ModTime().After(lastMod) {
			lastMod = st.ModTime()
			f.load(twoFeeds()) // the rewrite is picked up
		}
	}
	g, _ = runningGateway(t, fake)
	if err := g.Apply(first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := g.Apply(twoFeeds()); err != nil { // ~100 ms later at the venue
		t.Fatal(err)
	}
	waitFor(t, "confidence loaded", func() bool { return strings.Join(fake.names(), ",") == "confidence,program" })
	if !fake.hasUser("confidence") {
		t.Fatal("confidence's publisher not loaded")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, c := range fake.calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Errorf("went through the API although the rewrite was picked up: %s", c)
		}
	}
}

// A watcher that never reloads: the missing path and user are pushed
// through the API instead, and a removed feed is taken out.
func TestVerifyPushesThroughAPI(t *testing.T) {
	first := twoFeeds()
	first.Video.Feeds = first.Video.Feeds[:1]
	fake := &fakeConfigAPI{}
	fake.load(first)
	g, _ := runningGateway(t, fake)
	if err := g.Apply(twoFeeds()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "confidence added", func() bool { return strings.Join(fake.names(), ",") == "confidence,program" })
	waitFor(t, "confidence publisher", func() bool { return fake.hasUser("confidence") })
	fake.mu.Lock()
	if fake.paths["confidence"]["source"] != "publisher" {
		t.Errorf("confidence path: %v", fake.paths["confidence"])
	}
	for _, u := range fake.users {
		if u.User == "confidence" && u.Pass != hashed("fedcba9876543210fedcba9876543210") {
			t.Errorf("confidence pass is not its key's hash: %q", u.Pass)
		}
	}
	fake.mu.Unlock()

	// remove program: the stale path and user go too
	only := twoFeeds()
	only.Video.Feeds = only.Video.Feeds[1:]
	if err := g.Apply(only); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "program removed", func() bool { return strings.Join(fake.names(), ",") == "confidence" })
	waitFor(t, "program publisher removed", func() bool { return !fake.hasUser("program") })
}

// Bounded: a gateway that refuses every repair is not hammered.
func TestVerifyGivesUp(t *testing.T) {
	var mu sync.Mutex
	writes := 0
	fake := &fakeConfigAPI{}
	fake.load(settings.Settings{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mu.Lock()
			writes++
			mu.Unlock()
			http.Error(w, `{"error":"no"}`, 400)
			return
		}
		fake.ServeHTTP(w, r)
	})
	g, _ := runningGateway(t, h)
	if err := g.Apply(twoFeeds()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * g.settle)
	mu.Lock()
	n := writes
	mu.Unlock()
	if n == 0 || n > 10 {
		t.Fatalf("%d repair calls, want a few", n)
	}
	time.Sleep(10 * g.settle)
	mu.Lock()
	defer mu.Unlock()
	if writes != n {
		t.Fatalf("kept retrying: %d → %d", n, writes)
	}
}

// Not running (or not managed): nothing is checked.
func TestVerifyIdleWhenStopped(t *testing.T) {
	called := false
	g, _ := runningGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	g.mu.Lock()
	g.state = "starting"
	g.mu.Unlock()
	g.Apply(twoFeeds())
	time.Sleep(5 * g.settle)
	if called {
		t.Fatal("checked a gateway that isn't running")
	}
}
