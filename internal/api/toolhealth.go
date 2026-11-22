package api

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
	"ptz/internal/devices"
)

// A tool connection (a general add-on connection) is "connected" when its
// add-on opened it, but it can still be broken: a Rumble link that expired,
// a key that's wrong. Settings asks its state now and then (in the
// background, never holding up the list) and shows the add-on's error, so
// the list doesn't say "connected" in green for something that isn't.

const (
	toolHealthEvery   = 15 * time.Second
	toolHealthTimeout = 4 * time.Second
)

type toolHealthEntry struct {
	err     string
	at      time.Time
	running bool
	impl    any // the connection the answer is for (a reopen starts over)
}

var toolHealth = struct {
	sync.Mutex
	m map[string]*toolHealthEntry
}{m: map[string]*toolHealthEntry{}}

// toolProblem is the last problem the add-on reported for l ("" none or
// not known yet), refreshed in the background when it's old.
func toolProblem(l *devices.Live, caps []string) string {
	if l.Impl == nil || !slices.Contains(caps, addon.CapTool) {
		return ""
	}
	impl := l.Impl
	toolHealth.Lock()
	defer toolHealth.Unlock()
	e := toolHealth.m[l.ID]
	if e == nil || e.impl != l.Impl {
		e = &toolHealthEntry{impl: l.Impl}
		toolHealth.m[l.ID] = e
	}
	if !e.running && time.Since(e.at) > toolHealthEvery {
		e.running = true
		go func(id string, e *toolHealthEntry) {
			ctx, cancel := context.WithTimeout(context.Background(), toolHealthTimeout)
			_, err := addon.Dispatch(ctx, impl, "addon.state", nil)
			timedOut := ctx.Err() != nil // before cancel, which sets it
			cancel()
			toolHealth.Lock()
			defer toolHealth.Unlock()
			e.running, e.at = false, time.Now()
			e.err = ""
			if err != nil && !timedOut {
				e.err = err.Error()
			}
		}(l.ID, e)
	}
	return e.err
}
