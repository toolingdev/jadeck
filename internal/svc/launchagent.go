package svc

// A per-user LaunchAgent on macOS (~/Library/LaunchAgents/<label>.plist,
// run in the logged-in user's session) rather than the system
// LaunchDaemon `ptz service install` makes. The service library only
// knows the daemon, so stopping and starting an agent install for an
// update failed and the update rolled back. launchd tells the job its own
// label in XPC_SERVICE_NAME; the update helper inherits it.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var labelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,200}$`)

// launchAgent is the job this process runs under when it's a user
// LaunchAgent: its label and plist path. ok is false otherwise.
func launchAgent() (label, plist string, ok bool) {
	if runtime.GOOS != "darwin" || os.Getuid() == 0 {
		return "", "", false
	}
	label = os.Getenv("XPC_SERVICE_NAME")
	if label == "" || label == "0" || !labelRE.MatchString(label) || strings.HasPrefix(label, "application.") {
		return "", "", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}
	plist = filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if _, err := os.Stat(plist); err != nil {
		return "", "", false
	}
	return label, plist, true
}

// launchctl is swapped in tests.
var launchctl = func(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

func agentControl(action, label, plist string) error {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	job := domain + "/" + label
	var args []string
	switch action {
	case "stop":
		args = []string{"bootout", job}
	case "start":
		args = []string{"bootstrap", domain, plist}
	case "restart":
		args = []string{"kickstart", "-k", job}
	default:
		return errors.New("a LaunchAgent install is managed by its plist (" + plist + "); " + action + " isn't supported here")
	}
	out, err := launchctl(args...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// already stopped / already loaded: the state asked for is there
		if (action == "stop" && strings.Contains(msg, "No such process")) ||
			(action == "start" && strings.Contains(msg, "already")) {
			return nil
		}
		return fmt.Errorf("launchctl %s: %v: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}

func agentStatus(label string) (string, error) {
	out, err := launchctl("print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
	if err != nil {
		return "stopped", nil
	}
	if strings.Contains(string(out), "state = running") {
		return "running", nil
	}
	return "stopped", nil
}
