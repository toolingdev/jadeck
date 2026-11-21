// Package addontest runs an add-on's Spec over an in-memory pipe, the
// way the controller talks to it, so add-on tests need no controller.
package addontest

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// Harness plays the controller side.
type Harness struct {
	t    *testing.T
	in   *io.PipeWriter
	mu   sync.Mutex
	wait map[int64]chan addon.Message
	next atomic.Int64
	Logs []addon.LogParams
	// Changed collects connection ids the add-on said changed.
	Changed []string
}

// Start serves spec and initializes it (failing the test if the
// manifest is invalid).
func Start(t *testing.T, spec addon.Spec) *Harness {
	t.Helper()
	if err := spec.Manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &Harness{t: t, in: inW, wait: map[int64]chan addon.Message{}}
	done := make(chan struct{})
	go func() {
		addon.ServeIO(context.Background(), spec, inR, outW)
		outW.Close()
		close(done)
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for sc.Scan() {
			var m addon.Message
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			if m.ID != nil {
				h.mu.Lock()
				ch := h.wait[*m.ID]
				delete(h.wait, *m.ID)
				h.mu.Unlock()
				if ch != nil {
					ch <- m
				}
				continue
			}
			h.mu.Lock()
			switch m.Method {
			case "log":
				var l addon.LogParams
				json.Unmarshal(m.Params, &l)
				h.Logs = append(h.Logs, l)
			case "changed":
				var c struct{ Conn string }
				json.Unmarshal(m.Params, &c)
				h.Changed = append(h.Changed, c.Conn)
			}
			h.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	var res addon.InitializeResult
	h.Request("initialize", addon.InitializeParams{APIVersion: addon.APIVersion, Host: "addontest"}, &res)
	if res.Manifest.ID != spec.Manifest.ID {
		t.Fatalf("initialize returned manifest %q", res.Manifest.ID)
	}
	return h
}

// Request sends one request and decodes its result; an error fails the test.
func (h *Harness) Request(method string, params, out any) {
	h.t.Helper()
	if err := h.TryRequest(method, params, out); err != nil {
		h.t.Fatalf("%s: %v", method, err)
	}
}

// TryRequest is Request that returns the add-on's error.
func (h *Harness) TryRequest(method string, params, out any) error {
	id := h.next.Add(1)
	ch := make(chan addon.Message, 1)
	h.mu.Lock()
	h.wait[id] = ch
	h.mu.Unlock()
	b, _ := json.Marshal(params)
	msg, _ := json.Marshal(addon.Message{JSONRPC: "2.0", ID: &id, Method: method, Params: b})
	if _, err := h.in.Write(append(msg, '\n')); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-time.After(20 * time.Second):
		h.t.Fatalf("%s: no answer", method)
		return nil
	}
}

// Open opens a connection; it returns its capabilities.
func (h *Harness) Open(conn, typ string, config map[string]any) []string {
	h.t.Helper()
	cfg, _ := json.Marshal(config)
	var r addon.OpenResult
	h.Request("open", addon.OpenParams{Conn: conn, Type: typ, Config: cfg}, &r)
	return r.Capabilities
}

// Call runs a call method on a connection.
func (h *Harness) Call(conn, method string, params, out any) {
	h.t.Helper()
	if err := h.TryCall(conn, method, params, out); err != nil {
		h.t.Fatalf("%s: %v", method, err)
	}
}

// TryCall is Call that returns the add-on's error.
func (h *Harness) TryCall(conn, method string, params, out any) error {
	b, _ := json.Marshal(params)
	return h.TryRequest("call", addon.CallParams{Conn: conn, Method: method, Params: b}, out)
}

// Resource fetches a resource the way the controller does for
// "res:<path>" state values (addon.resource).
func (h *Harness) Resource(conn, path string) (contentType string, data []byte, err error) {
	var r addon.ResourceResult
	if err := h.TryCall(conn, "addon.resource", addon.ResourceParams{Path: path}, &r); err != nil {
		return "", nil, err
	}
	return r.ContentType, r.Data, nil
}

// ResourceResult is Resource with the whole answer (maxAge included).
func (h *Harness) ResourceResult(conn, path string) (addon.ResourceResult, error) {
	var r addon.ResourceResult
	err := h.TryCall(conn, "addon.resource", addon.ResourceParams{Path: path}, &r)
	return r, err
}

// Options asks for a live option list the way the controller does: conn
// empty for a connection settings form, a connection id for a widget
// field (the connection's Optioner answers first).
func (h *Harness) Options(name, conn string) []addon.Option {
	h.t.Helper()
	var o []addon.Option
	h.Request("options", addon.OptionsParams{Name: name, Conn: conn}, &o)
	return o
}

// State reads addon.state for a widget with these settings (nil: no
// widget, as for a plain state request).
func (h *Harness) State(conn string, settings map[string]any) map[string]any {
	h.t.Helper()
	var st map[string]any
	var params any
	if settings != nil {
		params = addon.StateParams{Settings: settings}
	}
	h.Call(conn, "addon.state", params, &st)
	return st
}
