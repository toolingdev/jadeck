package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// asOutsider replays a request as if it came from the internet.
func (e *env) asOutsider(t *testing.T, method, path, key string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.RemoteAddr = "203.0.113.9:5555"
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code
}

func TestScopeMapCoversEveryRoute(t *testing.T) {
	cases := map[string]string{
		"GET /api/status":            "camera.read",
		"POST /api/presets/3/recall": "camera.move",
		"POST /api/presets/3/store":  "presets.edit",
		"DELETE /api/presets/3":      "presets.edit",
		"GET /api/presets":           "camera.read",
		"POST /api/camera/wb-pick":   "camera.image",
		"GET /api/camera/image":      "camera.read",
		"POST /api/vmix/cut":         "switcher",
		"POST /api/proclaim/next":    "slides",
		"POST /api/wing/mute":        "mixer",
		"POST /api/macros/4/run":     "macros.run",
		"POST /api/macros/import":    "macros.edit",
		"POST /api/update/apply":     "update.apply",
		"POST /api/keys":             scopeTrusted,
		"GET /api/audit":             scopeTrusted,
		"GET /api/brand-new-thing":   scopeTrusted,
		"GET /":                      scopePublic,
		"GET /static/app.js":         scopePublic,
	}
	for route, want := range cases {
		m, p, _ := strings.Cut(route, " ")
		if got := scopeFor(m, p); got != want {
			t.Errorf("%s: %q want %q", route, got, want)
		}
	}
}

func TestAccessPolicy(t *testing.T) {
	e := setup(t)
	// Trusted network, no key: full access, including key admin.
	if code, _, _ := e.do(t, "GET", "/api/status", nil); code != 200 {
		t.Fatalf("LAN status %d", code)
	}
	code, r, _ := e.do(t, "POST", "/api/keys", map[string]any{"name": "Claude", "scopes": []string{"camera.read", "slides"}})
	if code != 201 {
		t.Fatalf("create key %d %v", code, r)
	}
	secret := r["secret"].(string)
	id := r["key"].(map[string]any)["id"].(float64)

	// Outside, no key: refused. Pages still load.
	if c := e.asOutsider(t, "GET", "/api/status", ""); c != 401 {
		t.Fatalf("outsider no key: %d", c)
	}
	if c := e.asOutsider(t, "GET", "/", ""); c != 200 {
		t.Fatalf("outsider page: %d", c)
	}
	// Outside with key: scoped.
	if c := e.asOutsider(t, "GET", "/api/status", secret); c != 200 {
		t.Fatalf("key read: %d", c)
	}
	if c := e.asOutsider(t, "POST", "/api/ptz/home", secret); c != 403 {
		t.Fatalf("key without camera.move: %d", c)
	}
	if c := e.asOutsider(t, "POST", "/api/presentation/next", secret); c != 200 {
		t.Fatalf("slides: %d", c)
	}
	// A key can never manage keys, even from the LAN.
	req, _ := http.NewRequest("GET", e.url+"/api/keys", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("key admin with a key: %d", resp.StatusCode)
	}
	// A key on the LAN is still held to its scopes.
	req, _ = http.NewRequest("POST", e.url+"/api/ptz/home", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("LAN key exceeded scope: %d", resp.StatusCode)
	}
	// Toggle a permission on, then turn the key off, revoke, delete.
	sid := fmtID(id)
	e.do(t, "PATCH", "/api/keys/"+sid, map[string]any{"scopes": []string{"camera.read", "camera.move"}})
	if c := e.asOutsider(t, "POST", "/api/ptz/home", secret); c != 200 {
		t.Fatalf("after granting camera.move: %d", c)
	}
	e.do(t, "PATCH", "/api/keys/"+sid, map[string]any{"enabled": false})
	if c := e.asOutsider(t, "GET", "/api/status", secret); c != 401 {
		t.Fatalf("disabled key: %d", c)
	}
	e.do(t, "POST", "/api/keys/"+sid+"/revoke", nil)
	if code, _, _ := e.do(t, "PATCH", "/api/keys/"+sid, map[string]any{"enabled": true}); code != 400 {
		t.Fatalf("re-enable revoked: %d", code)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/keys/"+sid, nil); code != 200 {
		t.Fatal("delete")
	}
	if c := e.asOutsider(t, "GET", "/api/status", secret); c != 401 {
		t.Fatalf("deleted key: %d", c)
	}
	_, _, audit := e.do(t, "GET", "/api/audit", nil)
	if len(audit) == 0 {
		t.Fatal("no audit entries")
	}
}

func fmtID(f float64) string { return strconv.FormatInt(int64(f), 10) }

type bearerRT struct {
	key  string
	base http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.key != "" {
		r.Header.Set("Authorization", "Bearer "+b.key)
	}
	return b.base.RoundTrip(r)
}

func mcpSession(t *testing.T, e *env, key string) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	return c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   e.url + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{key, http.DefaultTransport}},
		MaxRetries: -1, DisableStandaloneSSE: true,
	}, nil)
}

func TestMCP(t *testing.T) {
	e := setup(t)
	if _, err := mcpSession(t, e, ""); err == nil {
		t.Fatal("MCP without a key connected (even from localhost it must need one)")
	}
	_, r, _ := e.do(t, "POST", "/api/keys", map[string]any{"name": "agent",
		"scopes": []string{"camera.read", "camera.move", "mixer", "macros.edit", "macros.run"}})
	key := r["secret"].(string)
	cs, err := mcpSession(t, e, key)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	ctx := context.Background()
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range lt.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"camera_status", "camera_move", "preset_recall", "mixer_mute", "macro_save", "devices_info"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	for _, hidden := range []string{"switcher_cut", "slides_action", "update_apply", "image_settings_set", "preset_save"} {
		if names[hidden] {
			t.Errorf("tool %s visible without its permission", hidden)
		}
	}
	call := func(name string, args any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}
	text := func(r *mcp.CallToolResult) string {
		if len(r.Content) == 0 {
			return ""
		}
		if tc, ok := r.Content[0].(*mcp.TextContent); ok {
			return tc.Text
		}
		return ""
	}
	if r := call("camera_status", map[string]any{}); r.IsError || !strings.Contains(text(r), `"connected": true`) {
		t.Fatalf("status: %v", text(r))
	}
	if r := call("camera_snapshot", map[string]any{}); r.IsError || len(r.Content) == 0 {
		t.Fatal("snapshot")
	} else if _, ok := r.Content[0].(*mcp.ImageContent); !ok {
		t.Fatal("snapshot is not an image")
	}
	before := e.mock.State().Pan
	if r := call("camera_move", map[string]any{"pan_degrees": 10, "tilt_degrees": 0}); r.IsError {
		t.Fatalf("move: %s", text(r))
	}
	if moved := e.mock.State().Pan - before; moved < 130 || moved > 160 { // 10° × 14.4
		t.Fatalf("moved %d units", moved)
	}
	if r := call("camera_move", map[string]any{"pan_degrees": 500}); !r.IsError {
		t.Fatal("unbounded move accepted")
	}
	if r := call("mixer_mute", map[string]any{"favorite": "VOX", "muted": true}); r.IsError {
		t.Fatalf("mute: %s", text(r))
	}
	if m, _ := e.mixer.GetMute(ctx, 8); !m {
		t.Fatal("VOX not muted")
	}
	if r := call("macro_save", map[string]any{"macro": map[string]any{"name": "Quiet", "steps": []any{
		map[string]any{"type": "mixer_mute", "channelName": "MAIN", "muted": true}}}}); r.IsError {
		t.Fatalf("macro_save: %s", text(r))
	}
	if r := call("macro_run", map[string]any{"name": "quiet"}); r.IsError || !strings.Contains(text(r), `"ok": true`) {
		t.Fatalf("macro_run: %s", text(r))
	}
	if r := call("preset_recall", map[string]any{"name": "Nonexistent"}); !r.IsError {
		t.Fatal("unknown preset accepted")
	}
	// Revoking the key cuts the agent off at once.
	_, kl, _ := e.do(t, "GET", "/api/keys", nil)
	id := kl["keys"].([]any)[0].(map[string]any)["id"].(float64)
	e.do(t, "POST", "/api/keys/"+fmtID(id)+"/revoke", nil)
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "camera_status", Arguments: map[string]any{}}); err == nil {
		t.Fatal("revoked key still works over MCP")
	}
	_, _, audit := e.do(t, "GET", "/api/audit", nil)
	found := false
	for _, a := range audit {
		if strings.HasPrefix(a.(map[string]any)["action"].(string), "mcp camera_move") {
			found = true
		}
	}
	if !found {
		t.Fatal("MCP actions not audited")
	}
}

func TestDashboardRoutes(t *testing.T) {
	e := setup(t)
	_, _, l := e.do(t, "GET", "/api/dashboards", nil)
	if len(l) != 1 || l[0].(map[string]any)["kind"] != "tablet" {
		t.Fatalf("seed: %v", l)
	}
	booth := l[0].(map[string]any)
	_, defs, _ := e.do(t, "GET", "/api/dashboards/defaults", nil)
	if defs["phone"] != booth["id"] {
		t.Fatalf("defaults: %v", defs)
	}
	code, c, _ := e.do(t, "POST", "/api/dashboards", map[string]any{"name": "Audio", "kind": "phone", "from": booth["id"]})
	if code != 201 || c["kind"] != "phone" || len(c["layout"].([]any)) != len(booth["widgets"].([]any)) {
		t.Fatalf("copy as phone: %d %v", code, c)
	}
	id := fmtID(c["id"].(float64))
	code, d, _ := e.do(t, "PUT", "/api/dashboards/defaults", map[string]any{"phone": c["id"]})
	if code != 200 || d["phone"] != c["id"] {
		t.Fatalf("set default: %d %v", code, d)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/dashboards/"+id, nil); code != 400 {
		t.Fatalf("deleted a default: %d", code)
	}
	code, n, _ := e.do(t, "POST", "/api/dashboards", map[string]any{"name": "Empty desk", "kind": "desktop", "widgets": []any{}, "layout": []any{}})
	if code != 201 || n["kind"] != "desktop" {
		t.Fatalf("%d %v", code, n)
	}
	if code, _, _ := e.do(t, "POST", "/api/dashboards", map[string]any{"name": "x", "kind": "watch"}); code != 400 {
		t.Fatalf("bad kind: %d", code)
	}
	_, kr, _ := e.do(t, "POST", "/api/keys", map[string]any{"name": "viewer", "scopes": []string{"camera.read"}})
	if c := e.asOutsider(t, "GET", "/api/dashboards", kr["secret"].(string)); c != 200 {
		t.Fatalf("key read: %d", c)
	}
	if c := e.asOutsider(t, "PUT", "/api/dashboards/defaults", kr["secret"].(string)); c != 403 {
		t.Fatalf("key set defaults: %d", c)
	}
	e.do(t, "PUT", "/api/dashboards/defaults", map[string]any{"phone": booth["id"]})
	if code, _, _ := e.do(t, "DELETE", "/api/dashboards/"+id, nil); code != 200 {
		t.Fatal("delete")
	}
}
