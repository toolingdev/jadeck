package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
	"ptz/internal/devices"
)

type brokenTool struct{}

func (brokenTool) AddonState(context.Context) (map[string]any, error) {
	return nil, errors.New("Rumble: link expired")
}

// A tool connection's problem shows in Settings (found in the background,
// never holding up the list).
func TestToolProblem(t *testing.T) {
	toolHealth.Lock()
	delete(toolHealth.m, "r1") // fresh for -count > 1
	toolHealth.Unlock()
	l := &devices.Live{Connection: devices.Connection{ID: "r1"}, Impl: brokenTool{}}
	if p := toolProblem(l, []string{addon.CapTool}); p != "" {
		t.Fatalf("first look should not wait: %q", p)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p := toolProblem(l, []string{addon.CapTool}); p != "" {
			if p != "Rumble: link expired" {
				t.Fatalf("problem %q", p)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("problem never reported")
}
