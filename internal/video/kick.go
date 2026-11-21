package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"ptz/internal/settings"
)

// Cutting off a push feed's sender. MediaMTX checks credentials only
// when a session starts, so a sender already publishing with an old key
// stays on air after the key changes. CutPublishers puts the new user
// list into effect through the control API (the same list the config
// file holds, so the file watcher's own reload changes nothing), then
// kicks every session publishing on the path: a reconnect with the old
// key is refused.

// publisherLists are the control API's session lists that can hold a
// publisher (a disabled server answers 404 and is skipped).
var publisherLists = []string{"rtspsessions", "rtspssessions", "rtmpconns", "rtmpsconns", "srtconns", "webrtcsessions"}

// Kicked is one publishing session that was disconnected.
type Kicked struct {
	Kind string // rtspsessions, srtconns, …
	ID   string
}

// Running reports whether this app's own gateway process is up (not just
// something answering the API port).
func (g *Gateway) Running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state == "running" && g.cmd != nil
}

func (g *Gateway) apiBase() string {
	if g.API != "" {
		return "http://" + g.API
	}
	return "http://" + apiAddr
}

func (g *Gateway) apiDo(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.apiBase()+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, b, nil
}

// CutPublishers applies s's publish credentials to the running gateway
// and disconnects every session publishing on path. It returns the
// sessions it kicked; an error means the gateway could not be reached
// or refused the new credentials (nothing was kicked then).
func (g *Gateway) CutPublishers(ctx context.Context, s settings.Settings, path string) ([]Kicked, error) {
	code, b, err := g.apiDo(ctx, http.MethodPatch, "/v3/config/global/patch",
		map[string]any{"authInternalUsers": authUsers(s)})
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("gateway refused the new credentials: HTTP %d %s", code, bytes.TrimSpace(b))
	}
	var out []Kicked
	var firstErr error
	for _, kind := range publisherLists {
		code, b, err := g.apiDo(ctx, http.MethodGet, "/v3/"+kind+"/list?itemsPerPage=1000", nil)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if code != http.StatusOK {
			continue // that server is off
		}
		var list struct {
			Items []struct {
				ID    string `json:"id"`
				State string `json:"state"`
				Path  string `json:"path"`
			} `json:"items"`
		}
		if err := json.Unmarshal(b, &list); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", kind, err)
			}
			continue
		}
		for _, it := range list.Items {
			if it.Path != path || it.State != "publish" {
				continue
			}
			code, b, err := g.apiDo(ctx, http.MethodPost, "/v3/"+kind+"/kick/"+url.PathEscape(it.ID), nil)
			switch {
			case err != nil:
				if firstErr == nil {
					firstErr = err
				}
			case code != http.StatusOK && code != http.StatusNotFound: // 404: it just left
				if firstErr == nil {
					firstErr = fmt.Errorf("%s kick: HTTP %d %s", kind, code, bytes.TrimSpace(b))
				}
			default:
				out = append(out, Kicked{Kind: kind, ID: it.ID})
			}
		}
	}
	return out, firstErr
}
