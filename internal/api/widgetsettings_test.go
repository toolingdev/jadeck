package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
	"ptz/internal/dashboards"
)

// console is a v2.1 connection: live options, state per widget, and a
// meters resource that must not be cached.
type console struct {
	gearSlides
	mu       sync.Mutex
	settings []map[string]any
	reads    atomic.Int32
	slow     time.Duration
}

func (c *console) AddonOptions(_ context.Context, name string) ([]addon.Option, error) {
	if name == "channels" {
		return []addon.Option{{Value: "ch1", Label: "Ch 1 · Speaker"}, {Value: "dca2", Label: "DCA 2 · Band"}}, nil
	}
	return nil, nil
}

func (c *console) AddonState(context.Context) (map[string]any, error) {
	return map[string]any{"plain": true}, nil
}

func (c *console) AddonStateFor(_ context.Context, s map[string]any) (map[string]any, error) {
	c.mu.Lock()
	c.settings = append(c.settings, s)
	c.mu.Unlock()
	return map[string]any{"settings": s}, nil
}

func (c *console) ResourceResult(_ context.Context, path string) (addon.ResourceResult, error) {
	n := c.reads.Add(1)
	time.Sleep(c.slow)
	switch path {
	case "meters":
		return addon.ResourceResult{ContentType: "application/octet-stream", Data: []byte{byte(n)}, MaxAge: addon.Age(0)}, nil
	case "logo.png":
		return addon.ResourceResult{ContentType: "image/png", Data: []byte("L"), MaxAge: addon.Age(60)}, nil
	}
	return addon.ResourceResult{}, addon.ErrNotFound
}

func TestWidgetSettingsAndOptions(t *testing.T) {
	e := setup(t)
	code, r, _ := e.do(t, "POST", "/api/connections", map[string]any{"addon": "testgear", "type": "console", "name": "WING"})
	if code != 201 {
		t.Fatalf("add: %d %v", code, r)
	}
	id := r["id"].(string)

	// Options: ?conn asks the connection; without it, the add-on-wide list.
	var opts []addon.Option
	_, body := get(t, e.url+"/api/connections/options/testgear/channels?conn="+id)
	json.Unmarshal([]byte(body), &opts)
	if len(opts) != 2 || opts[0].Label != "Ch 1 · Speaker" {
		t.Fatalf("conn options %s", body)
	}
	_, body = get(t, e.url+"/api/connections/options/testgear/channels")
	if json.Unmarshal([]byte(body), &opts); len(opts) != 1 || opts[0].Value != "x" {
		t.Fatalf("plain options %s", body)
	}
	// a connection of another add-on, or none, falls back too
	_, body = get(t, e.url+"/api/connections/options/testgear/channels?conn=nope")
	if json.Unmarshal([]byte(body), &opts); len(opts) != 1 {
		t.Fatalf("unknown conn options %s", body)
	}

	// State for a widget: its saved settings (declared fields only,
	// defaults filled in) reach the add-on.
	d, err := e.srv.Dashboards.Create(&dashboards.Dashboard{Name: "Booth", Kind: "desktop", Widgets: []dashboards.Widget{
		{ID: "m1", Type: "addon:testgear:mutes", Settings: map[string]any{"connection": id, "channels": []any{"dca2", "ch1"}, "junk": 1}},
		{ID: "l1", Type: "addon:testgear:items", Settings: map[string]any{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	_, body = get(t, e.url+"/api/connections/"+id+"/state?widget=m1")
	json.Unmarshal([]byte(body), &st)
	set, _ := st["settings"].(map[string]any)
	if set == nil || set["size"] != "s" || set["junk"] != nil || len(set["channels"].([]any)) != 2 || set["channels"].([]any)[0] != "dca2" {
		t.Fatalf("state for m1: %s", body)
	}
	_, body = get(t, e.url+"/api/connections/"+id+"/state?widget=m1&dash="+strconv.FormatInt(d.ID, 10))
	if json.Unmarshal([]byte(body), &st); st["settings"] == nil {
		t.Fatalf("state for m1 on dash: %s", body)
	}
	// no widget, an unknown one, one without fields, another dashboard: plain state
	for _, q := range []string{"", "?widget=nope", "?widget=l1", "?widget=m1&dash=999999"} {
		_, body = get(t, e.url+"/api/connections/"+id+"/state"+q)
		if json.Unmarshal([]byte(body), &st); st["plain"] != true {
			t.Errorf("state%s: %s", q, body)
		}
	}
}

func TestResourceMaxAgeAndCoalescing(t *testing.T) {
	e := setup(t)
	code, r, _ := e.do(t, "POST", "/api/connections", map[string]any{"addon": "testgear", "type": "console", "name": "WING"})
	if code != 201 {
		t.Fatalf("add: %d %v", code, r)
	}
	id := r["id"].(string)
	c := e.srv.Devs().Get(id).Impl.(*console)
	base := e.url + "/api/connections/" + id + "/res/"
	resources.mu.Lock() // connection ids repeat between tests: start empty
	resources.m, resources.flights, resources.bytes = nil, nil, 0
	resources.mu.Unlock()

	// maxAge 60: cached, and said so.
	resp, _ := get(t, base+"logo.png")
	if resp.Header.Get("Cache-Control") != "private, max-age=60" {
		t.Fatalf("logo cache-control %q", resp.Header.Get("Cache-Control"))
	}
	get(t, base+"logo.png")
	if n := c.reads.Load(); n != 1 {
		t.Fatalf("logo read %d times", n)
	}

	// maxAge 0: never cached; requests 100 ms apart each read.
	c.reads.Store(0)
	resp, _ = get(t, base+"meters")
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("meters cache-control %q", resp.Header.Get("Cache-Control"))
	}
	time.Sleep(100 * time.Millisecond)
	get(t, base+"meters")
	if n := c.reads.Load(); n != 2 {
		t.Fatalf("meters 100 ms apart read %d times, want 2", n)
	}

	// Five screens asking at once: one read.
	c.reads.Store(0)
	c.slow = 30 * time.Millisecond
	time.Sleep(60 * time.Millisecond) // past the last window
	var wg sync.WaitGroup
	bodies := make([]string, 5)
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(base + "meters")
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			b := make([]byte, 4)
			n, _ := resp.Body.Read(b)
			bodies[i] = string(b[:n])
		}()
	}
	wg.Wait()
	if n := c.reads.Load(); n != 1 {
		t.Fatalf("5 at once read %d times, want 1", n)
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Fatalf("different answers %q", bodies)
		}
	}
}

// The coalescing window itself, on a fake clock.
func TestResCacheWindow(t *testing.T) {
	now := time.Unix(0, 0)
	c := &resCache{now: func() time.Time { return now }}
	calls := 0
	get := func() (resEntry, error) {
		calls++
		return resEntry{data: []byte{byte(calls)}, until: now}, nil // maxAge 0
	}
	c.fetch("k", get)
	now = now.Add(49 * time.Millisecond)
	if e, _ := c.fetch("k", get); calls != 1 || e.data[0] != 1 {
		t.Fatalf("within 50 ms: %d calls", calls)
	}
	now = now.Add(2 * time.Millisecond)
	if e, _ := c.fetch("k", get); calls != 2 || e.data[0] != 2 {
		t.Fatalf("after 50 ms: %d calls", calls)
	}
	if len(c.m) != 0 {
		t.Fatalf("maxAge 0 was cached: %v", c.m)
	}
	if resTTL(nil) != 2*time.Second || resTTL(addon.Age(0)) != 0 || resTTL(addon.Age(-1)) != 0 || resTTL(addon.Age(1e6)) != time.Hour {
		t.Fatal("resTTL")
	}
}
