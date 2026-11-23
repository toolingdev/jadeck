package svc

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLaunchAgentControl(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getuid() == 0 {
		t.Skip("LaunchAgents are a macOS, non-root thing")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755)
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.example.jadeck.plist")
	os.WriteFile(plist, []byte("<plist/>"), 0o644)

	// not under launchd, or a label with no plist: the service library's way
	t.Setenv("XPC_SERVICE_NAME", "0")
	if _, _, ok := launchAgent(); ok {
		t.Fatal("XPC_SERVICE_NAME=0 is not an agent")
	}
	t.Setenv("XPC_SERVICE_NAME", "com.example.other")
	if _, _, ok := launchAgent(); ok {
		t.Fatal("no plist, no agent")
	}
	t.Setenv("XPC_SERVICE_NAME", "com.example.jadeck")
	label, p, ok := launchAgent()
	if !ok || label != "com.example.jadeck" || p != plist {
		t.Fatalf("agent = %q %q %v", label, p, ok)
	}

	var calls []string
	orig, origSleep := launchctl, sleep
	defer func() { launchctl, sleep = orig, origSleep }()
	sleep = func(time.Duration) {}
	booted := 0
	launchctl = func(args ...string) ([]byte, error) {
		if args[0] == "print" {
			return nil, fmt.Errorf("gone") // the job is unloaded at once
		}
		if args[0] == "bootstrap" {
			if booted++; booted == 1 {
				calls = append(calls, "bootstrap (refused)")
				return []byte("Bootstrap failed: 5: Input/output error"), fmt.Errorf("exit 5")
			}
		}
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "bootout" && len(calls) > 4 {
			return []byte("Boot-out failed: 3: No such process"), fmt.Errorf("exit 3")
		}
		return nil, nil
	}
	for _, a := range []string{"stop", "start", "restart", "stop"} {
		if err := Control(a, []string{"run"}); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	uid := os.Getuid()
	want := []string{
		fmt.Sprintf("bootout gui/%d/com.example.jadeck", uid),
		"bootstrap (refused)",
		fmt.Sprintf("bootstrap gui/%d %s", uid, plist),
		fmt.Sprintf("kickstart -k gui/%d/com.example.jadeck", uid),
		fmt.Sprintf("bootout gui/%d/com.example.jadeck", uid),
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}
