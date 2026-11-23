package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
	"ptz/internal/addons"
)

// Add-on widgets v2 (design/WIDGETS-V2.md): pictures an add-on's state
// points at ("res:<path>"), and the ui/ files frame widgets load.

// ------------------------------------------------------------ resources

// resPrefix marks a state value that names one of the connection's
// resources; connection state rewrites it to the res route.
const resPrefix = "res:"

// rewriteRes returns state with every string "res:<rest>" (anywhere in
// the tree) turned into "/api/connections/<id>/res/<rest>", so widgets can
// use it as an image URL. The tree is first brought to plain JSON values.
func rewriteRes(connID string, state any) (any, error) {
	b, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(b, []byte(`"`+resPrefix)) {
		return json.RawMessage(b), nil // nothing to rewrite: send as is
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // keep big integers exact
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	base := "/api/connections/" + url.PathEscape(connID) + "/res/"
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			if rest, ok := strings.CutPrefix(t, resPrefix); ok && rest != "" {
				return base + strings.TrimPrefix(rest, "/")
			}
		case map[string]any:
			for k, e := range t {
				t[k] = walk(e)
			}
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
		}
		return x
	}
	return walk(v), nil
}

// resourceOf is how a connection serves resources: add-on processes and
// v2.1 connections as an addon.ResultResourcer (with maxAge), older
// in-process connections as an addon.Resourcer.
func resourceOf(impl any) func(ctx context.Context, path string) (addon.ResourceResult, error) {
	if r, ok := impl.(addon.ResultResourcer); ok {
		return r.ResourceResult
	}
	if r, ok := impl.(addon.Resourcer); ok {
		return func(ctx context.Context, path string) (addon.ResourceResult, error) {
			ct, data, err := r.Resource(ctx, path)
			return addon.ResourceResult{ContentType: ct, Data: data}, err
		}
	}
	return nil
}

// coalesce: requests for the same (connection, path) that arrive within
// this long of the first share its one add-on call, even for a resource
// that must not be cached (maxAge 0): five screens polling meters 30
// times a second cost one read.
const coalesce = 50 * time.Millisecond

// resTTL is how long an answer is kept: the add-on's maxAge (seconds; 0 =
// not kept), else addon.DefaultMaxAge.
func resTTL(maxAge *int) time.Duration {
	if maxAge == nil {
		return addon.DefaultMaxAge * time.Second
	}
	return time.Duration(min(max(*maxAge, 0), addon.MaxMaxAge)) * time.Second
}

type resEntry struct {
	ct     string
	data   []byte
	maxAge int // seconds, for Cache-Control
	until  time.Time
}

// flight is one add-on call that later requests for the same key join.
type flight struct {
	start time.Time
	done  chan struct{}
	e     resEntry
	err   error
}

// resCache keeps resources for their TTL so several screens showing the
// same slide ask the add-on once, and joins requests in flight.
type resCache struct {
	mu      sync.Mutex
	m       map[string]resEntry
	flights map[string]*flight
	bytes   int
	now     func() time.Time // tests
}

const resCacheMax = 32 << 20

func (c *resCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// fetch returns the cached answer for key, joins a call for it made in
// the last 50 ms (or still running), or calls get.
func (c *resCache) fetch(key string, get func() (resEntry, error)) (resEntry, error) {
	c.mu.Lock()
	now := c.clock()
	if e, ok := c.m[key]; ok && now.Before(e.until) {
		c.mu.Unlock()
		return e, nil
	}
	if f, ok := c.flights[key]; ok {
		select {
		case <-f.done:
			if now.Sub(f.start) < coalesce {
				c.mu.Unlock()
				return f.e, f.err
			}
		default: // still running: join it
			c.mu.Unlock()
			<-f.done
			return f.e, f.err
		}
	}
	if c.flights == nil {
		c.flights = map[string]*flight{}
	}
	for k, f := range c.flights { // forget finished calls past the window
		select {
		case <-f.done:
			if now.Sub(f.start) >= coalesce {
				delete(c.flights, k)
			}
		default:
		}
	}
	f := &flight{start: now, done: make(chan struct{})}
	c.flights[key] = f
	c.mu.Unlock()

	func() {
		defer close(f.done) // waiters never hang, even if get panics
		f.e, f.err = get()
		if f.err == nil {
			c.put(key, f.e)
		}
	}()
	return f.e, f.err
}

func (c *resCache) put(key string, e resEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	if c.m == nil {
		c.m = map[string]resEntry{}
	}
	for k, old := range c.m { // drop what's expired
		if !now.Before(old.until) {
			c.bytes -= len(old.data)
			delete(c.m, k)
		}
	}
	if old, ok := c.m[key]; ok {
		c.bytes -= len(old.data)
		delete(c.m, key)
	}
	if !now.Before(e.until) || c.bytes+len(e.data) > resCacheMax {
		return // not to be kept (maxAge 0), or no room
	}
	c.m[key] = e
	c.bytes += len(e.data)
}

var resources resCache

// errResource is an add-on answer the route turns into a status code.
type errResource struct {
	code int
	msg  string
}

func (e errResource) Error() string { return e.msg }

// GET /api/connections/{id}/res/{path...} (mux pattern {id}/{rest...},
// rest = "res/<path>"): the query string is part of
// the add-on's path ("slides/3.png?w=640").
func (s *Server) connectionResource(w http.ResponseWriter, r *http.Request) {
	p, isRes := strings.CutPrefix(r.PathValue("rest"), "res/")
	if !isRes {
		http.NotFound(w, r)
		return
	}
	l := s.Devs().Get(r.PathValue("id"))
	if l == nil {
		fail(w, 404, "no such connection")
		return
	}
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || path.Clean("/"+p) != "/"+p {
		fail(w, 404, "no such resource")
		return
	}
	if r.URL.RawQuery != "" {
		p += "?" + r.URL.RawQuery
	}
	if l.Impl == nil {
		fail(w, 503, l.Name+" is not connected: "+errText(l.Err))
		return
	}
	get := resourceOf(l.Impl)
	if get == nil {
		fail(w, 404, "this connection has no resources")
		return
	}
	e, err := resources.fetch(l.ID+"\x00"+p, func() (resEntry, error) {
		// Not this request's context: others may be waiting on the call.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := get(ctx, p)
		switch {
		case errors.Is(err, addon.ErrNoResources):
			return resEntry{}, errResource{404, "this connection has no resources"}
		case errors.Is(err, addon.ErrNotFound):
			return resEntry{}, errResource{404, "no such resource"}
		case err != nil:
			return resEntry{}, errResource{502, err.Error()}
		case len(res.Data) > addon.MaxResource:
			return resEntry{}, errResource{502, "the add-on's resource is over 4 MB"}
		}
		if res.ContentType == "" {
			res.ContentType = "application/octet-stream"
		}
		ttl := resTTL(res.MaxAge)
		return resEntry{ct: res.ContentType, data: res.Data, maxAge: int(ttl / time.Second), until: resources.clock().Add(ttl)}, nil
	})
	if err != nil {
		var er errResource
		if errors.As(err, &er) {
			fail(w, er.code, er.msg)
		} else {
			fail(w, 502, err.Error())
		}
		return
	}
	h := w.Header()
	h.Set("Content-Type", e.ct)
	if e.maxAge > 0 {
		h.Set("Cache-Control", "private, max-age="+strconv.Itoa(e.maxAge))
	} else {
		h.Set("Cache-Control", "no-store")
	}
	h.Set("X-Content-Type-Options", "nosniff")
	// Opened directly, an add-on's HTML or SVG can't run script here.
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox")
	w.Write(e.data)
}

// -------------------------------------------------------- frame ui files

// uiCSP is the policy frame widgets' files are served with.
const uiCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'none'; frame-ancestors 'self'"

// uiCSPFor is uiCSP with this jadeck's own address named next to 'self'.
// Frames run sandboxed (no allow-same-origin), so their origin is opaque,
// and Safari (iOS 26) matches 'self' against that opaque origin: nothing,
// so a frame's own scripts and styles were refused and the widget stayed
// blank. Naming the host the page was asked for (both schemes: behind a
// tunnel the request arrives over http for an https page) lets them load
// everywhere; it allows nothing 'self' didn't mean already.
func uiCSPFor(r *http.Request) string {
	host := r.Host
	if host == "" || !cspHostOK(host) {
		return uiCSP
	}
	src := "'self' http://" + host + " https://" + host
	return "default-src 'none'; script-src " + src + "; style-src " + src + " 'unsafe-inline'; img-src " + src + " data: blob:; font-src " + src + "; connect-src 'none'; frame-ancestors " + src
}

// cspHostOK: a Host header that can't break out of a CSP source list
// (letters, digits, dots, dashes, a port, an IPv6 literal).
func cspHostOK(h string) bool {
	if len(h) > 255 {
		return false
	}
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == ':', c == '[', c == ']':
		default:
			return false
		}
	}
	return true
}

var uiTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".woff2": "font/woff2",
	".json":  "application/json",
}

// GET /api/addons/{id}/ui/{path...}
func (s *Server) addonUI(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", uiCSPFor(r))
	h.Set("X-Content-Type-Options", "nosniff")
	if s.Addons == nil {
		fail(w, 404, "not found")
		return
	}
	name := r.PathValue("path")
	b, mod, err := s.Addons.UIFile(r.PathValue("id"), name)
	if errors.Is(err, addons.ErrNotFound) {
		fail(w, 404, "not found")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ct := uiTypes[strings.ToLower(path.Ext(name))]
	if ct == "" {
		fail(w, 404, "not found")
		return
	}
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "no-cache") // revalidate: dev add-ons change under us
	http.ServeContent(w, r, "", mod, bytes.NewReader(b))
}
