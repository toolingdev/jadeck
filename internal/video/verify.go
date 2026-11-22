package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"ptz/internal/settings"
)

// Verify and repair. MediaMTX reloads mediamtx.yml when the file
// changes, but its watcher drops any change that lands within a second
// of the last one it acted on (no trailing reload). Two feeds created
// back to back left the second one out of the running gateway: its path
// and its publisher missing, and the sender refused. So after every
// write the running config is read back through the control API and
// compared with what was written; if it differs, the file is written
// again (a fresh change for the watcher) and, if that is not picked up
// either, the differences are pushed through the API. Bounded retries;
// the next settings change starts a fresh round.

const (
	verifySettle   = 1500 * time.Millisecond // > the watcher's 1 s window
	verifyAttempts = 3
)

func (g *Gateway) pokeVerify() {
	g.mu.Lock()
	g.init()
	ch := g.verifyCh
	g.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (g *Gateway) settleTime() time.Duration {
	if g.settle > 0 {
		return g.settle
	}
	return verifySettle
}

// pathConf is one gateway path as written to the config file (and as
// pushed through the API on a repair).
type pathConf struct {
	Name          string
	Source        string
	RTSPTransport string // "" or "tcp"
	OnDemand      bool
}

func (p pathConf) body() map[string]any {
	b := map[string]any{"source": p.Source, "sourceOnDemand": p.OnDemand}
	if p.RTSPTransport != "" {
		b["rtspTransport"] = p.RTSPTransport
	}
	return b
}

// pathConfs are the paths Config(s) writes, in the same order.
func pathConfs(s settings.Settings) []pathConf {
	var out []pathConf
	for _, c := range s.Cameras {
		if !c.Video {
			continue
		}
		out = append(out,
			pathConf{Name: PathHD(c.ID), Source: c.RTSP(c.StreamHD), RTSPTransport: "tcp", OnDemand: true},
			pathConf{Name: PathSD(c.ID), Source: c.RTSP(c.StreamSD), RTSPTransport: "tcp", OnDemand: true})
	}
	for _, f := range s.Video.Feeds {
		switch f.Source {
		case "push":
			out = append(out, pathConf{Name: f.ID, Source: "publisher"})
		case "pull":
			if f.URL == "" {
				continue
			}
			p := pathConf{Name: f.ID, Source: f.URL, OnDemand: true}
			if strings.HasPrefix(f.URL, "rtsp") {
				p.RTSPTransport = "tcp"
			}
			out = append(out, p)
		}
	}
	return out
}

// srtWanted reports whether the config written for s turns SRT on.
func (g *Gateway) srtWanted(s settings.Settings) bool {
	return strings.Contains(g.configText(s), "\nsrt: yes\n")
}

// drift is how the running gateway differs from the written config.
type drift struct {
	Missing []string // paths not loaded
	Stale   []string // paths loaded with another source
	Extra   []string // paths still loaded that were removed
	Users   []string // publish users missing, extra or with other permissions
	SRT     bool     // the SRT server's on/off differs
}

func (d drift) empty() bool {
	return len(d.Missing)+len(d.Stale)+len(d.Extra)+len(d.Users) == 0 && !d.SRT
}

func (d drift) String() string {
	var parts []string
	add := func(what string, v []string) {
		if len(v) > 0 {
			parts = append(parts, what+" "+strings.Join(v, ","))
		}
	}
	add("missing paths", d.Missing)
	add("changed paths", d.Stale)
	add("removed paths still loaded", d.Extra)
	add("publish users", d.Users)
	if d.SRT {
		parts = append(parts, "SRT server on/off")
	}
	return strings.Join(parts, "; ")
}

// check compares the running gateway's config with s. Passwords are not
// compared (the API redacts them); a key change is put into effect
// through the API by CutPublishers anyway.
func (g *Gateway) check(ctx context.Context, s settings.Settings) (drift, error) {
	var d drift
	code, b, err := g.apiDo(ctx, http.MethodGet, "/v3/config/paths/list?itemsPerPage=1000", nil)
	if err != nil {
		return d, err
	}
	if code != http.StatusOK {
		return d, fmt.Errorf("paths list: HTTP %d %s", code, bytes.TrimSpace(b))
	}
	var pl struct {
		Items []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &pl); err != nil {
		return d, fmt.Errorf("paths list: %w", err)
	}
	have := map[string]string{}
	for _, it := range pl.Items {
		have[it.Name] = it.Source
	}
	want := map[string]bool{}
	for _, p := range pathConfs(s) {
		want[p.Name] = true
		src, ok := have[p.Name]
		switch {
		case !ok:
			d.Missing = append(d.Missing, p.Name)
		case src != p.Source:
			d.Stale = append(d.Stale, p.Name)
		}
	}
	for name := range have {
		if !want[name] && name != "all" && name != "all_others" && !strings.HasPrefix(name, "~") {
			d.Extra = append(d.Extra, name)
		}
	}

	code, b, err = g.apiDo(ctx, http.MethodGet, "/v3/config/global/get", nil)
	if err != nil {
		return d, err
	}
	if code != http.StatusOK {
		return d, fmt.Errorf("global config: HTTP %d %s", code, bytes.TrimSpace(b))
	}
	var gl struct {
		SRT   *bool `json:"srt"`
		Users []struct {
			User        string     `json:"user"`
			Permissions []authPerm `json:"permissions"`
		} `json:"authInternalUsers"`
	}
	if err := json.Unmarshal(b, &gl); err != nil {
		return d, fmt.Errorf("global config: %w", err)
	}
	// publish users: name → the paths they may publish
	pubs := func(users []authUser) map[string]string {
		m := map[string]string{}
		for _, u := range users {
			var ps []string
			for _, p := range u.Permissions {
				if p.Action == "publish" {
					ps = append(ps, p.Path)
				}
			}
			if len(ps) > 0 {
				sort.Strings(ps)
				m[u.User] = strings.Join(ps, ",")
			}
		}
		return m
	}
	running := make([]authUser, 0, len(gl.Users))
	for _, u := range gl.Users {
		running = append(running, authUser{User: u.User, Permissions: u.Permissions})
	}
	wantU, haveU := pubs(authUsers(s)), pubs(running)
	for u, ps := range wantU {
		if haveU[u] != ps {
			d.Users = append(d.Users, u)
		}
	}
	for u := range haveU {
		if _, ok := wantU[u]; !ok {
			d.Users = append(d.Users, u)
		}
	}
	if gl.SRT != nil && *gl.SRT != g.srtWanted(s) {
		d.SRT = true
	}
	sort.Strings(d.Missing)
	sort.Strings(d.Stale)
	sort.Strings(d.Extra)
	sort.Strings(d.Users)
	return d, nil
}

// pushDrift puts s into effect through the control API for what d says
// differs. The file already holds the same, so a later reload of it
// changes nothing.
func (g *Gateway) pushDrift(ctx context.Context, s settings.Settings, d drift) error {
	var firstErr error
	note := func(what string, code int, b []byte, err error) {
		if firstErr != nil {
			return
		}
		if err != nil {
			firstErr = fmt.Errorf("%s: %w", what, err)
		} else if code != http.StatusOK {
			firstErr = fmt.Errorf("%s: HTTP %d %s", what, code, bytes.TrimSpace(b))
		}
	}
	if len(d.Users) > 0 || d.SRT {
		patch := map[string]any{"authInternalUsers": authUsers(s)}
		if d.SRT {
			patch["srt"] = g.srtWanted(s)
		}
		code, b, err := g.apiDo(ctx, http.MethodPatch, "/v3/config/global/patch", patch)
		note("global patch", code, b, err)
	}
	byName := map[string]pathConf{}
	for _, p := range pathConfs(s) {
		byName[p.Name] = p
	}
	for _, n := range d.Missing {
		code, b, err := g.apiDo(ctx, http.MethodPost, "/v3/config/paths/add/"+url.PathEscape(n), byName[n].body())
		note("add path "+n, code, b, err)
	}
	for _, n := range d.Stale {
		code, b, err := g.apiDo(ctx, http.MethodPost, "/v3/config/paths/replace/"+url.PathEscape(n), byName[n].body())
		note("replace path "+n, code, b, err)
	}
	for _, n := range d.Extra {
		code, b, err := g.apiDo(ctx, http.MethodDelete, "/v3/config/paths/delete/"+url.PathEscape(n), nil)
		if code == http.StatusNotFound {
			continue // already gone
		}
		note("remove path "+n, code, b, err)
	}
	return firstErr
}

// verifyLoop checks the running gateway after every config write (once
// the writes settle) and repairs it when it missed one. It runs beside
// Run until ctx ends.
func (g *Gateway) verifyLoop(ctx context.Context) {
	g.mu.Lock()
	g.init()
	ch := g.verifyCh
	g.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
		}
		if !g.waitSettled(ctx, ch) {
			return
		}
		g.verifyOnce(ctx, ch)
	}
}

// waitSettled waits until no write came for a settle period.
func (g *Gateway) waitSettled(ctx context.Context, ch chan struct{}) bool {
	t := time.NewTimer(g.settleTime())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ch:
			if !t.Stop() {
				<-t.C
			}
			t.Reset(g.settleTime())
		case <-t.C:
			return true
		}
	}
}

// verifyOnce runs one bounded check-and-repair round. A new write
// during it ends the round (the loop starts a fresh one for it).
func (g *Gateway) verifyOnce(ctx context.Context, ch chan struct{}) {
	log := g.logger()
	for attempt := 1; attempt <= verifyAttempts; attempt++ {
		g.mu.Lock()
		want := g.expect
		g.mu.Unlock()
		if want == nil || !want.Video.Managed || !g.Running() {
			return
		}
		s := *want
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		d, err := g.check(cctx, s)
		cancel()
		if err != nil {
			log.Debug("gateway config not checked", "err", err)
			return
		}
		if d.empty() {
			if attempt > 1 {
				log.Info("stream gateway config repaired", "attempts", attempt-1)
			}
			return
		}
		log.Warn("stream gateway missed a config change", "drift", d.String(), "attempt", attempt)
		if attempt == 1 {
			// a fresh change for its file watcher (the last one fell
			// inside its one-second window)
			if err := g.rewrite(); err != nil {
				log.Error("stream gateway config not rewritten", "err", err)
			}
		} else {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := g.pushDrift(cctx, s, d)
			cancel()
			if err != nil {
				log.Warn("stream gateway config not repaired through its API", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ch:
			// settings changed again: a fresh round follows
			select {
			case ch <- struct{}{}:
			default:
			}
			return
		case <-time.After(g.settleTime()):
		}
	}
	g.mu.Lock()
	want := g.expect
	g.mu.Unlock()
	if want != nil {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		d, err := g.check(cctx, *want)
		cancel()
		if err == nil && !d.empty() {
			log.Error("stream gateway config could not be repaired", "drift", d.String())
		} else if err == nil {
			log.Info("stream gateway config repaired", "attempts", verifyAttempts)
		}
	}
}

// rewrite writes the latest applied settings' config again.
func (g *Gateway) rewrite() error {
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	g.mu.Lock()
	want := g.expect
	g.mu.Unlock()
	if want == nil {
		return nil
	}
	return g.writeConfig(*want)
}

// verifyPath asks for a check now: the gateway refused a sender for a
// path that should exist.
func (g *Gateway) verifyPath(name string) {
	g.mu.Lock()
	want := g.expect
	g.mu.Unlock()
	if want == nil {
		return
	}
	for _, p := range pathConfs(*want) {
		if p.Name == name {
			g.pokeVerify()
			return
		}
	}
}
