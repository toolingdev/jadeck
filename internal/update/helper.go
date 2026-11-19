package update

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ServiceControl is injected by cmd/ptz (keeps this package free of the
// service library): action is "stop" or "start".
var ServiceControl func(action string, args []string) error

type helperArgs struct {
	exe, newFile, sha, version, from, health, state, mode string
	execArgs, svcArgs                                     []string
}

// RunHelper is `ptz _apply ...`: runs from a COPY of the old binary,
// swaps in the new one, health-checks it, and rolls back on failure.
// Every outcome is written to the state file for the UI.
func RunHelper(argv []string) error {
	fs := flag.NewFlagSet("_apply", flag.ContinueOnError)
	var a helperArgs
	var ea, sa string
	fs.StringVar(&a.exe, "exe", "", "")
	fs.StringVar(&a.newFile, "new", "", "")
	fs.StringVar(&a.sha, "sha", "", "")
	fs.StringVar(&a.version, "version", "", "")
	fs.StringVar(&a.from, "from", "", "")
	fs.StringVar(&a.health, "health", "", "")
	fs.StringVar(&a.state, "state", "", "")
	fs.StringVar(&a.mode, "mode", "exec", "")
	fs.StringVar(&ea, "exec-args", "[]", "")
	fs.StringVar(&sa, "service-args", "[]", "")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	json.Unmarshal([]byte(ea), &a.execArgs)
	json.Unmarshal([]byte(sa), &a.svcArgs)
	logf := func(f string, v ...any) {
		fmt.Printf("%s helper: %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(f, v...))
	}
	write := func(state, reason string) {
		b, _ := json.Marshal(HelperState{State: state, Version: a.version, From: a.from, Reason: reason, At: time.Now().Format(time.RFC3339)})
		writeAtomic(a.state, b, 0o644)
		logf("%s %s", state, reason)
	}

	// 1. Second verification, from the helper's side.
	if got, err := fileSHA(a.newFile); err != nil || got != a.sha {
		write("failed", "staged binary changed after verification — not applied")
		return errors.New("hash mismatch")
	}
	// 2. Stop the running build.
	var child *os.Process
	stop := func() {
		if a.mode == "service" && ServiceControl != nil {
			if err := ServiceControl("stop", a.svcArgs); err != nil {
				logf("service stop: %v", err)
			}
		} else if child != nil {
			child.Kill()
		}
		waitDown(a.health, 20*time.Second)
	}
	start := func() error {
		if a.mode == "service" && ServiceControl != nil {
			return ServiceControl("start", a.svcArgs)
		}
		cmd := exec.Command(a.exe, a.execArgs...)
		cmd.Dir = filepath.Dir(a.exe)
		if lf, err := os.OpenFile(filepath.Join(filepath.Dir(a.state), "ptz.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			cmd.Stdout, cmd.Stderr = lf, lf
		}
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		child = cmd.Process
		return nil
	}
	stop()
	// 3. Swap, keeping the old build as <exe>.old for rollback.
	old := a.exe + ".old"
	os.Remove(old)
	if err := os.Rename(a.exe, old); err != nil {
		write("failed", "could not move the current build aside: "+err.Error())
		start()
		return err
	}
	if err := copyFile(a.newFile, a.exe, 0o755); err != nil {
		os.Rename(old, a.exe)
		write("rolled_back", "could not install the new build: "+err.Error())
		start()
		return err
	}
	// 4. Start the new build and require it to report the new version.
	if err := start(); err != nil {
		logf("start new: %v", err)
	}
	if v, err := waitUp(a.health, 60*time.Second); err == nil && v == a.version {
		write("applied", "")
		os.Remove(a.newFile)
		os.Remove(filepath.Join(filepath.Dir(a.state), "staged.json"))
		return nil
	} else {
		reason := fmt.Sprintf("the new build did not come up healthy within 60 s (%v)", err)
		if err == nil {
			reason = fmt.Sprintf("the new build reported version %q, expected %q", v, a.version)
		}
		logf("rolling back: %s", reason)
		// 5. Roll back.
		stop()
		os.Remove(a.exe)
		if rerr := os.Rename(old, a.exe); rerr != nil {
			write("failed", reason+"; rollback could not restore the old build: "+rerr.Error())
			return rerr
		}
		start()
		if _, err := waitUp(a.health, 60*time.Second); err != nil {
			blockVersion(filepath.Dir(a.state), a.version)
			write("failed", reason+"; the restored build did not come back either")
			return err
		}
		write("rolled_back", reason)
		// Never offer this build again; the restored build reads the block.
		blockVersion(filepath.Dir(a.state), a.version)
		os.Remove(a.newFile)
		os.Remove(filepath.Join(filepath.Dir(a.state), "staged.json"))
		return errors.New(reason)
	}
}

func probe(url string) (string, error) {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var v struct {
		Version string `json:"version"`
	}
	json.Unmarshal(b, &v)
	return v.Version, nil
}

func waitDown(url string, max time.Duration) {
	dl := time.Now().Add(max)
	for time.Now().Before(dl) {
		if _, err := probe(url); err != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func waitUp(url string, max time.Duration) (string, error) {
	dl := time.Now().Add(max)
	var last error
	for time.Now().Before(dl) {
		v, err := probe(url)
		if err == nil {
			return v, nil
		}
		last = err
		time.Sleep(500 * time.Millisecond)
	}
	return "", last
}
