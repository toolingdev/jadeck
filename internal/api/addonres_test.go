package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// picSlides is a slides connection whose state points at pictures it
// serves (addon.resource), like Proclaim's slide thumbnails.
type picSlides struct {
	gearSlides
	served int
}

func (p *picSlides) AddonState(context.Context) (map[string]any, error) {
	return map[string]any{
		"live":   "res:slides/1.png?w=640",
		"big":    9007199254740993, // stays exact
		"plain":  "resolution: high",
		"thumbs": []map[string]any{{"id": "a", "src": "res:slides/2.png"}, {"id": "b", "src": "data:image/png;base64,AA=="}},
	}, nil
}

func (p *picSlides) Resource(_ context.Context, path string) (string, []byte, error) {
	p.served++
	switch path {
	case "slides/1.png?w=640":
		return "image/png", []byte("PNG-1"), nil
	case "slides/2.png":
		return "image/png", []byte("PNG-2"), nil
	case "page.html":
		return "text/html", []byte("<script>alert(1)</script>"), nil
	}
	return "", nil, addon.ErrNotFound
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestConnectionResources(t *testing.T) {
	resources = resCache{} // the cache is package-wide and connection ids repeat between runs
	e := setup(t)
	code, r, _ := e.do(t, "POST", "/api/connections", map[string]any{"addon": "testgear", "type": "pics", "name": "Proclaim"})
	if code != 201 {
		t.Fatalf("add: %d %v", code, r)
	}
	id := r["id"].(string)

	// State: "res:" values become this connection's res route, anywhere.
	resp, body := get(t, e.url+"/api/connections/"+id+"/state")
	var st map[string]any
	json.Unmarshal([]byte(body), &st)
	base := "/api/connections/" + id + "/res/"
	if st["live"] != base+"slides/1.png?w=640" || st["plain"] != "resolution: high" {
		t.Fatalf("state %v", st)
	}
	thumbs := st["thumbs"].([]any)
	if thumbs[0].(map[string]any)["src"] != base+"slides/2.png" || !strings.HasPrefix(thumbs[1].(map[string]any)["src"].(string), "data:") {
		t.Fatalf("thumbs %v", thumbs)
	}
	if !strings.Contains(body, "9007199254740993") {
		t.Fatalf("big number changed: %s", body)
	}

	// The rewritten URL serves the picture, cached for 2 s.
	resp, body = get(t, e.url+st["live"].(string))
	if resp.StatusCode != 200 || body != "PNG-1" || resp.Header.Get("Content-Type") != "image/png" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Cache-Control"), "max-age=2") {
		t.Fatalf("resource: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	get(t, e.url+st["live"].(string))
	if n := e.srv.Devs().Get(id).Impl.(*picSlides).served; n != 1 {
		t.Fatalf("add-on asked %d times within 2 s", n)
	}
	// An add-on's HTML can't run script from our origin.
	resp, _ = get(t, e.url+base+"page.html")
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("html resource without sandbox CSP: %v", resp.Header)
	}
	for _, p := range []string{"missing.png", "a/../b.png", "a//b.png"} {
		if resp, _ := get(t, e.url+base+p); resp.StatusCode != 404 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	if resp, _ := get(t, e.url+"/api/connections/nope/res/x.png"); resp.StatusCode != 404 {
		t.Errorf("unknown connection: %d", resp.StatusCode)
	}
	if resp, _ := get(t, e.url+"/api/connections/"+id+"/other/x.png"); resp.StatusCode != 404 {
		t.Errorf("not res: %d", resp.StatusCode)
	}

	// A connection without resources: 404.
	code, r, _ = e.do(t, "POST", "/api/connections", map[string]any{"addon": "testgear", "type": "sw", "name": "vMix"})
	if code != 201 {
		t.Fatalf("add sw: %d %v", code, r)
	}
	if resp, body := get(t, e.url+"/api/connections/"+r["id"].(string)+"/res/x.png"); resp.StatusCode != 404 || !strings.Contains(body, "no resources") {
		t.Fatalf("no resourcer: %d %s", resp.StatusCode, body)
	}

	// Outside the trusted network: the same permission as state.
	code, r, _ = e.do(t, "POST", "/api/keys", map[string]any{"name": "a", "scopes": []string{"addons"}})
	withAddons := r["secret"].(string)
	_, r, _ = e.do(t, "POST", "/api/keys", map[string]any{"name": "b", "scopes": []string{"camera.read"}})
	without := r["secret"].(string)
	if c := e.asOutsider(t, "GET", base+"slides/2.png", withAddons); c != 200 {
		t.Fatalf("key with addons: %d", c)
	}
	if c := e.asOutsider(t, "GET", base+"slides/2.png", without); c != 403 {
		t.Fatalf("key without addons: %d", c)
	}
	if c := e.asOutsider(t, "GET", base+"slides/2.png", ""); c != 401 {
		t.Fatalf("no key: %d", c)
	}
}

func TestAddonUIFiles(t *testing.T) {
	e := setup(t)
	e.srv.Addons.SetInProcessUI("testgear", fstest.MapFS{
		"live.html":  {Data: []byte("<!doctype html><script src=app.js></script>")},
		"app.js":     {Data: []byte("parent.postMessage({t:'ready'},'*')")},
		"s.css":      {Data: []byte("body{}")},
		"f.woff2":    {Data: []byte("wOF2")},
		"img/a.svg":  {Data: []byte("<svg/>")},
		"img/b.png":  {Data: []byte("png")},
		"d.json":     {Data: []byte("{}")},
		"run.sh":     {Data: []byte("x")},
		"img/c.html": {Data: []byte("x")},
	})
	types := map[string]string{
		"live.html": "text/html; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "s.css": "text/css; charset=utf-8",
		"f.woff2": "font/woff2", "img/a.svg": "image/svg+xml", "img/b.png": "image/png", "d.json": "application/json",
	}
	for name, ct := range types {
		resp, _ := get(t, e.url+"/api/addons/testgear/ui/"+name)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ct {
			t.Errorf("%s: %d %q", name, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if resp.Header.Get("Content-Security-Policy") != uiCSP || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s headers %v", name, resp.Header)
		}
	}
	if uiCSP != "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'none'; frame-ancestors 'self'" {
		t.Fatal("CSP differs from the spec")
	}
	for _, p := range []string{"run.sh", "img", "img/", "../addon.json", "%2e%2e/addon.json", "missing.js"} {
		if resp, _ := get(t, e.url+"/api/addons/testgear/ui/"+p); resp.StatusCode != 404 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	if resp, _ := get(t, e.url+"/api/addons/nope/ui/live.html"); resp.StatusCode != 404 {
		t.Errorf("unknown add-on: %d", resp.StatusCode)
	}
	// Readable wherever dashboards are: any key will do outside.
	_, r, _ := e.do(t, "POST", "/api/keys", map[string]any{"name": "screen", "scopes": []string{"camera.read"}})
	if c := e.asOutsider(t, "GET", "/api/addons/testgear/ui/live.html", r["secret"].(string)); c != 200 {
		t.Fatalf("outsider with a key: %d", c)
	}
	if c := e.asOutsider(t, "GET", "/api/dashboards", r["secret"].(string)); c != 200 {
		t.Fatalf("dashboards with the same key: %d", c)
	}
}
