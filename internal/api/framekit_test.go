package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestFrameKit(t *testing.T) {
	e := setup(t)
	for name, ct := range map[string]string{"v1/kit.js": "text/javascript; charset=utf-8", "v1/kit.css": "text/css; charset=utf-8"} {
		resp, body := get(t, e.url+"/api/kit/"+name)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ct {
			t.Fatalf("%s: %d %q", name, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Cache-Control"), "max-age") {
			t.Errorf("%s headers %v", name, resp.Header)
		}
		if len(body) < 1000 {
			t.Errorf("%s: %d bytes", name, len(body))
		}
		// revalidation: same ETag → 304
		req, _ := http.NewRequest("GET", e.url+"/api/kit/"+name, nil)
		req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
		r2, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r2.Body.Close()
		if r2.StatusCode != 304 {
			t.Errorf("%s: If-None-Match gave %d", name, r2.StatusCode)
		}
	}
	if resp, body := get(t, e.url+"/api/kit/v1/kit.js"); !strings.Contains(body, `version: "1.`) {
		t.Errorf("kit.js has no v1 version (%d)", resp.StatusCode)
	}
	for _, p := range []string{"v1/", "v1/missing.js", "../framekit.go", "v9/kit.js"} {
		if resp, _ := get(t, e.url+"/api/kit/"+p); resp.StatusCode != 404 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	// No data in it: readable without a key, like the page itself.
	if c := e.asOutsider(t, "GET", "/api/kit/v1/kit.css", ""); c != 200 {
		t.Errorf("outsider, no key: %d", c)
	}
}
