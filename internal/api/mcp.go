package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ptz/internal/auth"
)

// MCP: Streamable HTTP at /mcp, API key required. Each request builds a
// server holding only the tools the key's permissions allow, so an agent
// never even sees a tool it can't use. Tools run through the same
// internal handlers as the web UI (same validation, same safety guards).

func (s *Server) mcpRoutes(mux *http.ServeMux) {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		k := KeyFrom(r.Context())
		if k == nil {
			return nil
		}
		return s.mcpServer(k, clientIP(r))
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.Handle("/mcp", h)
	mux.Handle("/mcp/", h)
}

// inner calls this server's own routes, bypassing the access layer
// (the caller already enforced the key's scope for the tool).
func (s *Server) inner(ctx context.Context, method, path string, body any) (int, []byte) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func detailOf(b []byte) string {
	var d struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(b, &d) == nil && d.Detail != "" {
		return d.Detail
	}
	return strings.TrimSpace(string(b))
}

func jsonResult(v any) *mcp.CallToolResult {
	var text string
	switch t := v.(type) {
	case []byte:
		var buf bytes.Buffer
		if json.Indent(&buf, t, "", "  ") == nil {
			text = buf.String()
		} else {
			text = string(t)
		}
	default:
		b, _ := json.MarshalIndent(v, "", "  ")
		text = string(b)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

type noArgs struct{}

func ptr(b bool) *bool { return &b }

type mcpTool struct {
	scope string
	tool  *mcp.Tool
	add   func(*mcp.Server, *mcp.Tool)
}

// addTool registers a typed tool that audits every call.
func addTool[In any](list *[]mcpTool, s *Server, k *auth.Key, ip, scope, name, title, desc string, readOnly bool,
	fn func(ctx context.Context, in In) (*mcp.CallToolResult, error)) {
	t := &mcp.Tool{Name: name, Description: desc,
		Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: readOnly, OpenWorldHint: ptr(true)}}
	if !readOnly {
		t.Annotations.DestructiveHint = ptr(true)
	}
	*list = append(*list, mcpTool{scope: scope, tool: t, add: func(srv *mcp.Server, t *mcp.Tool) {
		mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			res, err := fn(ctx, in)
			status := "ok"
			if err != nil {
				status = "error: " + err.Error()
			}
			if !readOnly || err != nil {
				s.Keys.Audit(k, ip, "mcp "+name, status)
			}
			return res, nil, err
		})
	}})
}

// call is the common tool body: hit an internal route, surface its
// error text to the agent on failure.
func (s *Server) call(ctx context.Context, method, path string, body any) (*mcp.CallToolResult, error) {
	code, b := s.inner(ctx, method, path, body)
	if code >= 400 {
		return nil, errors.New(detailOf(b))
	}
	return jsonResult(b), nil
}

func (s *Server) presetID(name string) (int64, error) {
	u, err := s.mainCam()
	if err != nil {
		return 0, err
	}
	l, err := u.Presets.List()
	if err != nil {
		return 0, err
	}
	for _, p := range l {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("no preset named %q", name)
}

func (s *Server) macroID(name string) (int64, error) {
	m, err := s.MacroStore.ByName(name)
	if err != nil {
		return 0, err
	}
	if m == nil {
		return 0, fmt.Errorf("no macro named %q", name)
	}
	return m.ID, nil
}

// ------------------------------------------------------------ inputs

type moveIn struct {
	PanDegrees  float64 `json:"pan_degrees" jsonschema:"degrees to pan, -90..90 (positive = right)"`
	TiltDegrees float64 `json:"tilt_degrees" jsonschema:"degrees to tilt, -60..60 (positive = up)"`
}
type zoomIn struct {
	Direction string `json:"direction" jsonschema:"in (tele) or out (wide)"`
	Ms        int    `json:"ms" jsonschema:"how long to zoom, 50..3000 ms"`
	Speed     int    `json:"speed,omitempty" jsonschema:"5..100, default 40"`
}
type nameIn struct {
	Name string `json:"name" jsonschema:"name (case-insensitive)"`
}
type renameIn struct {
	Name    string `json:"name" jsonschema:"current preset name"`
	NewName string `json:"new_name" jsonschema:"new preset name"`
}
type focusIn struct {
	Action string `json:"action" jsonschema:"auto, manual, onepush, near, far or stop"`
}
type imageIn struct {
	Settings map[string]any `json:"settings" jsonschema:"fields to change, e.g. {\"brightness\": 8, \"flip\": false}; call image_settings_get first for the fields this camera supports"`
}
type wbIn struct {
	X float64 `json:"x" jsonschema:"tap point, 0 (left) .. 1 (right)"`
	Y float64 `json:"y" jsonschema:"tap point, 0 (top) .. 1 (bottom)"`
}
type inputIn struct {
	Input int `json:"input" jsonschema:"switcher input number"`
}
type fadeIn struct {
	DurationMs int `json:"duration_ms,omitempty" jsonschema:"0..10000, default 1000"`
}
type actionIn struct {
	Action string `json:"action" jsonschema:"one of the actions listed by slides_status"`
}
type muteIn struct {
	Channel  int    `json:"channel,omitempty" jsonschema:"console channel number (or use favorite)"`
	Favorite string `json:"favorite,omitempty" jsonschema:"favorite label from mixer_status (or use channel)"`
	Muted    bool   `json:"muted" jsonschema:"true = mute, false = unmute"`
}
type macroSaveIn struct {
	Macro map[string]any `json:"macro" jsonschema:"macro document {name, description, steps}; replaces a macro with the same name. Step types: mixer_mute, switcher, slides, camera_preset, delay (see macros_list for examples)"`
}

// ------------------------------------------------------------ server

func (s *Server) mcpServer(k *auth.Key, ip string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "ptz-controller", Title: "PTZ Controller", Version: s.Version},
		&mcp.ServerOptions{Instructions: "Controls a venue AV booth: a PTZ camera, a video switcher, slides and an audio mixer. " +
			"Every change happens live in the room. Prefer reading status first; camera motion is bounded per call. " +
			"Mixer changes are verified by read-back from the console."})
	var tools []mcpTool
	a := func(scope, name, title, desc string, ro bool, fn func(context.Context, noArgs) (*mcp.CallToolResult, error)) {
		addTool(&tools, s, k, ip, scope, name, title, desc, ro, fn)
	}
	get := func(path string) func(context.Context, noArgs) (*mcp.CallToolResult, error) {
		return func(ctx context.Context, _ noArgs) (*mcp.CallToolResult, error) { return s.call(ctx, "GET", path, nil) }
	}
	post := func(path string) func(context.Context, noArgs) (*mcp.CallToolResult, error) {
		return func(ctx context.Context, _ noArgs) (*mcp.CallToolResult, error) {
			return s.call(ctx, "POST", path, nil)
		}
	}

	// camera.read
	a("camera.read", "camera_status", "Camera status", "Connection, power, pan/tilt/zoom position, focus mode and device info.", true, get("/api/status"))
	a("camera.read", "presets_list", "List presets", "Camera presets in service order (name, VISCA slot).", true, get("/api/presets"))
	a("camera.read", "image_settings_get", "Read picture settings", "Current image settings the camera reports (brightness, saturation, …).", true, get("/api/camera/image"))
	a("camera.read", "camera_snapshot", "Camera snapshot", "A JPEG still of what the camera sees right now.", true,
		func(ctx context.Context, _ noArgs) (*mcp.CallToolResult, error) {
			u, err := s.mainCam()
			if err != nil {
				return nil, err
			}
			b, err := u.Svc.FetchSnapshot(ctx)
			if err != nil || len(b) == 0 {
				return nil, fmt.Errorf("snapshot unavailable")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: b, MIMEType: "image/jpeg"}}}, nil
		})
	// camera.move
	addTool(&tools, s, k, ip, "camera.move", "camera_move", "Move camera", "Pan/tilt by a bounded number of degrees, then report where it settled.", false,
		func(ctx context.Context, in moveIn) (*mcp.CallToolResult, error) {
			u, err := s.mainCam()
			if err != nil {
				return nil, err
			}
			from, to, err := u.Svc.MoveRelative(in.PanDegrees, in.TiltDegrees)
			if err != nil {
				return nil, err
			}
			return jsonResult(map[string]any{"from": from, "to": to}), nil
		})
	addTool(&tools, s, k, ip, "camera.move", "camera_zoom", "Zoom camera", "Zoom in or out for a bounded time (≤ 3 s), then stop.", false,
		func(ctx context.Context, in zoomIn) (*mcp.CallToolResult, error) {
			dir := map[string]int{"in": 1, "tele": 1, "out": -1, "wide": -1}[strings.ToLower(in.Direction)]
			if dir == 0 {
				return nil, fmt.Errorf("direction must be in or out")
			}
			if in.Ms < 50 {
				return nil, fmt.Errorf("ms must be 50..3000")
			}
			sp := in.Speed
			if sp == 0 {
				sp = 40
			}
			u, err := s.mainCam()
			if err != nil {
				return nil, err
			}
			if err := u.Svc.ZoomFor(ctx, dir, time.Duration(in.Ms)*time.Millisecond, sp); err != nil {
				return nil, err
			}
			return s.call(ctx, "GET", "/api/status", nil)
		})
	a("camera.move", "camera_home", "Camera home", "Send the camera to its home position.", false, post("/api/ptz/home"))
	a("camera.move", "camera_stop", "Stop camera", "Stop all camera motion immediately.", false, post("/api/ptz/stop"))
	addTool(&tools, s, k, ip, "camera.move", "camera_focus", "Camera focus", "Focus control: auto, manual, onepush, near, far, stop.", false,
		func(ctx context.Context, in focusIn) (*mcp.CallToolResult, error) {
			return s.call(ctx, "POST", "/api/focus", map[string]string{"action": in.Action})
		})
	addTool(&tools, s, k, ip, "camera.move", "preset_recall", "Recall preset", "Move the camera to a named preset.", false,
		func(ctx context.Context, in nameIn) (*mcp.CallToolResult, error) {
			id, err := s.presetID(in.Name)
			if err != nil {
				return nil, err
			}
			return s.call(ctx, "POST", "/api/presets/"+strconv.FormatInt(id, 10)+"/recall", nil)
		})
	// camera.image
	addTool(&tools, s, k, ip, "camera.image", "image_settings_set", "Change picture settings", "Change image settings (read-modify-write; untouched settings stay as they are).", false,
		func(ctx context.Context, in imageIn) (*mcp.CallToolResult, error) {
			return s.call(ctx, "POST", "/api/camera/image", in.Settings)
		})
	addTool(&tools, s, k, ip, "camera.image", "white_balance_pick", "Pick white point", "Balance white on a point that should be white (x,y in 0..1). The camera briefly aims at it and returns.", false,
		func(ctx context.Context, in wbIn) (*mcp.CallToolResult, error) {
			return s.call(ctx, "POST", "/api/camera/wb-pick", map[string]float64{"x": in.X, "y": in.Y})
		})
	// presets.edit
	addTool(&tools, s, k, ip, "presets.edit", "preset_save", "Save preset", "Save the camera's current position as a named preset (creates it if new).", false,
		func(ctx context.Context, in nameIn) (*mcp.CallToolResult, error) {
			id, err := s.presetID(in.Name)
			if err != nil {
				code, b := s.inner(ctx, "POST", "/api/presets", map[string]string{"name": in.Name})
				if code >= 400 {
					return nil, errors.New(detailOf(b))
				}
				var p struct {
					ID int64 `json:"id"`
				}
				json.Unmarshal(b, &p)
				id = p.ID
			}
			return s.call(ctx, "POST", "/api/presets/"+strconv.FormatInt(id, 10)+"/store", nil)
		})
	addTool(&tools, s, k, ip, "presets.edit", "preset_rename", "Rename preset", "Rename a preset.", false,
		func(ctx context.Context, in renameIn) (*mcp.CallToolResult, error) {
			id, err := s.presetID(in.Name)
			if err != nil {
				return nil, err
			}
			return s.call(ctx, "PATCH", "/api/presets/"+strconv.FormatInt(id, 10), map[string]string{"name": in.NewName})
		})
	addTool(&tools, s, k, ip, "presets.edit", "preset_delete", "Delete preset", "Delete a preset from the list.", false,
		func(ctx context.Context, in nameIn) (*mcp.CallToolResult, error) {
			id, err := s.presetID(in.Name)
			if err != nil {
				return nil, err
			}
			return s.call(ctx, "DELETE", "/api/presets/"+strconv.FormatInt(id, 10), nil)
		})
	// switcher
	a("switcher", "switcher_status", "Switcher status", "Inputs, what is in preview and on program, stream/record state.", true, get("/api/switcher/status"))
	addTool(&tools, s, k, ip, "switcher", "switcher_preview", "Preview input", "Put an input in preview (not live).", false,
		func(ctx context.Context, in inputIn) (*mcp.CallToolResult, error) {
			return s.call(ctx, "POST", "/api/switcher/preview", map[string]int{"input": in.Input})
		})
	a("switcher", "switcher_cut", "Cut to program", "Take preview live instantly.", false, post("/api/switcher/cut"))
	addTool(&tools, s, k, ip, "switcher", "switcher_fade", "Fade to program", "Take preview live with a fade.", false,
		func(ctx context.Context, in fadeIn) (*mcp.CallToolResult, error) {
			d := in.DurationMs
			if d == 0 {
				d = 1000
			}
			return s.call(ctx, "POST", "/api/switcher/fade", map[string]int{"durationMs": d})
		})
	// slides
	a("slides", "slides_status", "Slides status", "Presentation driver, whether it can send, and its actions.", true, get("/api/presentation/status"))
	addTool(&tools, s, k, ip, "slides", "slides_action", "Slide action", "Send a presentation action such as next, prev, black, logo, clear.", false,
		func(ctx context.Context, in actionIn) (*mcp.CallToolResult, error) {
			return s.call(ctx, "POST", "/api/presentation/"+in.Action, nil)
		})
	// mixer
	a("mixer", "mixer_status", "Mixer status", "Console identity and the favorite channels with their mute state.", true, get("/api/mixer/status"))
	addTool(&tools, s, k, ip, "mixer", "mixer_mute", "Mute / unmute", "Mute or unmute a channel by number or favorite label; verified by read-back.", false,
		func(ctx context.Context, in muteIn) (*mcp.CallToolResult, error) {
			ch := in.Channel
			if in.Favorite != "" {
				f, ok := s.Devs().FavoriteByName(in.Favorite)
				if !ok || f.Channel == nil {
					return nil, fmt.Errorf("no assigned favorite named %q", in.Favorite)
				}
				ch = *f.Channel
			}
			if ch == 0 {
				return nil, fmt.Errorf("give a channel number or a favorite label")
			}
			return s.call(ctx, "POST", "/api/mixer/channel-mute", map[string]any{"channel": ch, "muted": in.Muted})
		})
	// macros
	a("macros.run", "macros_list", "List macros", "Saved macros with their steps and last run.", true, get("/api/macros"))
	addTool(&tools, s, k, ip, "macros.run", "macro_run", "Run macro", "Run a macro by name; returns the step-by-step report.", false,
		func(ctx context.Context, in nameIn) (*mcp.CallToolResult, error) {
			id, err := s.macroID(in.Name)
			if err != nil {
				return nil, err
			}
			return s.call(ctx, "POST", "/api/macros/"+strconv.FormatInt(id, 10)+"/run", nil)
		})
	addTool(&tools, s, k, ip, "macros.edit", "macro_save", "Save macro", "Create a macro, or replace the one with the same name.", false,
		func(ctx context.Context, in macroSaveIn) (*mcp.CallToolResult, error) {
			name, _ := in.Macro["name"].(string)
			if id, err := s.macroID(name); err == nil {
				return s.call(ctx, "PATCH", "/api/macros/"+strconv.FormatInt(id, 10), in.Macro)
			}
			return s.call(ctx, "POST", "/api/macros", in.Macro)
		})
	addTool(&tools, s, k, ip, "macros.edit", "macro_delete", "Delete macro", "Delete a macro by name.", false,
		func(ctx context.Context, in nameIn) (*mcp.CallToolResult, error) {
			id, err := s.macroID(in.Name)
			if err != nil {
				return nil, err
			}
			return s.call(ctx, "DELETE", "/api/macros/"+strconv.FormatInt(id, 10), nil)
		})
	// updates
	a("update.apply", "update_check", "Check for update", "Check GitHub for a newer signed release.", false, post("/api/update/check"))
	a("update.apply", "update_apply", "Apply update", "Install the verified staged update (restarts the controller; rolls back automatically on failure).", false, post("/api/update/apply"))
	// any key
	a(scopeAny, "devices_info", "Devices", "Which camera, switcher, slides and mixer drivers this install uses.", true, get("/api/devices"))
	a(scopeAny, "update_status", "Update status", "Current version and whether a verified update is staged.", true, get("/api/update/status"))

	for _, t := range tools {
		if t.scope == scopeAny || k.Has(t.scope) {
			t.add(srv, t.tool)
		}
	}
	return srv
}
