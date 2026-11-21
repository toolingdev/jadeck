package video

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeMediaMTX answers the control API calls CutPublishers makes.
type fakeMediaMTX struct {
	mu     sync.Mutex
	calls  []string // in order
	patch  map[string]json.RawMessage
	kicked []string
}

func (f *fakeMediaMTX) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	switch {
	case r.Method == "PATCH" && r.URL.Path == "/v3/config/global/patch":
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &f.patch); err != nil {
			http.Error(w, err.Error(), 400)
		}
	case r.Method == "GET" && r.URL.Path == "/v3/rtspsessions/list":
		io.WriteString(w, `{"items":[
			{"id":"r1","state":"publish","path":"program"},
			{"id":"r2","state":"read","path":"program"},
			{"id":"r3","state":"publish","path":"lobby"},
			{"id":"r4","state":"idle","path":""}]}`)
	case r.Method == "GET" && r.URL.Path == "/v3/srtconns/list":
		io.WriteString(w, `{"items":[{"id":"s1","state":"publish","path":"program"},{"id":"s2","state":"read","path":"program"}]}`)
	case r.Method == "GET" && r.URL.Path == "/v3/webrtcsessions/list":
		io.WriteString(w, `{"items":[{"id":"w1","state":"publish","path":"program"},{"id":"w2","state":"read","path":"program"}]}`)
	case r.Method == "POST" && strings.Contains(r.URL.Path, "/kick/"):
		f.kicked = append(f.kicked, strings.TrimPrefix(r.URL.Path, "/v3/"))
	default:
		http.NotFound(w, r) // rtsps / rtmp servers off
	}
}

func TestCutPublishers(t *testing.T) {
	fake := &fakeMediaMTX{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	g := &Gateway{API: strings.TrimPrefix(srv.URL, "http://")}
	s := feedSettings()
	s.Video.Feeds[0].Key = "fedcba9876543210fedcba9876543210" // the new key

	kicked, err := g.CutPublishers(context.Background(), s, "program")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, k := range kicked {
		got[k.Kind+"/"+k.ID] = true
	}
	want := []string{"rtspsessions/r1", "srtconns/s1", "webrtcsessions/w1"}
	if len(kicked) != len(want) {
		t.Fatalf("kicked %v, want %v", kicked, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("did not kick %s (kicked %v)", w, kicked)
		}
	}
	for _, k := range fake.kicked {
		if !strings.HasSuffix(k, "/kick/r1") && !strings.HasSuffix(k, "/kick/s1") && !strings.HasSuffix(k, "/kick/w1") {
			t.Errorf("kicked a reader or another path: %s", k)
		}
	}
	// the new credentials go in before any kick, so a reconnect with the old key fails
	if len(fake.calls) == 0 || fake.calls[0] != "PATCH /v3/config/global/patch" {
		t.Fatalf("credentials not applied first: %v", fake.calls)
	}
	var users []authUser
	if err := json.Unmarshal(fake.patch["authInternalUsers"], &users); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range users {
		if u.User == "program" {
			found = true
			if u.Pass != hashed(s.Video.Feeds[0].Key) {
				t.Errorf("program's pass is not the new key's hash: %q", u.Pass)
			}
			if len(u.Permissions) != 1 || u.Permissions[0] != (authPerm{Action: "publish", Path: "program"}) {
				t.Errorf("program permissions: %+v", u.Permissions)
			}
		}
		if u.User == "nokey" {
			t.Error("a keyless push feed got a publisher")
		}
		if strings.Contains(u.Pass, s.Video.Feeds[0].Key) {
			t.Error("the plain key was sent to the gateway")
		}
	}
	if !found {
		t.Fatalf("no publisher user for program: %+v", users)
	}
	if !strings.Contains(Config(s), "pass: '"+hashed(s.Video.Feeds[0].Key)+"'") {
		t.Error("config file and API user list disagree")
	}

	// a removed feed: its user is gone and its sender cut off
	fake.calls, fake.kicked = nil, nil
	s.Video.Feeds = s.Video.Feeds[1:]
	if _, err := g.CutPublishers(context.Background(), s, "program"); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(fake.patch["authInternalUsers"], &users)
	for _, u := range users {
		if u.User == "program" {
			t.Error("the removed feed still has a publisher user")
		}
	}
	if len(fake.kicked) != 3 {
		t.Errorf("kicked %v after removal", fake.kicked)
	}
}

func TestCutPublishersRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/kick/") {
			t.Error("kicked although the new credentials were refused")
		}
		if r.Method == "PATCH" {
			http.Error(w, `{"error":"nope"}`, 400)
			return
		}
		io.WriteString(w, `{"items":[{"id":"r1","state":"publish","path":"program"}]}`)
	}))
	defer srv.Close()
	g := &Gateway{API: strings.TrimPrefix(srv.URL, "http://")}
	if _, err := g.CutPublishers(context.Background(), feedSettings(), "program"); err == nil {
		t.Fatal("want an error when the gateway refuses the credentials")
	}
	// no gateway at all
	g = &Gateway{API: "127.0.0.1:1"}
	if _, err := g.CutPublishers(context.Background(), feedSettings(), "program"); err == nil {
		t.Fatal("want an error with no gateway")
	}
}
