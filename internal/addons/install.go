package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/toolingdev/ptz-controller/sdk/addon"
	"github.com/toolingdev/ptz-controller/sdk/addon/pack"
)

// Install puts a package in place: unpack beside the current version,
// check that it starts and answers, then swap. The old version is kept
// as <id>.previous for Roll back. allowUnverified must be true for a
// package not signed by the release key.
func (h *Host) Install(p *pack.Package, allowUnverified bool) error {
	if h.dir == "" {
		return errors.New("no add-on folder")
	}
	if !p.Verified && !allowUnverified {
		return pack.ErrUnverified
	}
	id := p.Manifest.ID
	stage := filepath.Join(h.dir, id+".new")
	os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	write := func(name string, b []byte, mode os.FileMode) error {
		return os.WriteFile(filepath.Join(stage, name), b, mode)
	}
	mb, _ := json.MarshalIndent(p.Manifest, "", "  ")
	if err := write("addon.json", mb, 0o644); err != nil {
		return err
	}
	if err := write(exeName(), p.Program, 0o755); err != nil {
		return err
	}
	for _, extra := range []string{"README.md", "icon.svg", "CHANGELOG.md"} {
		if b, ok := p.Files[extra]; ok {
			write(extra, b, 0o644)
		}
	}
	// Health check: it must start and answer initialize with the same manifest.
	if err := healthCheck(h, stage, p.Manifest); err != nil {
		os.RemoveAll(stage)
		return fmt.Errorf("the new %s add-on didn't start, nothing changed: %w", p.Manifest.Name, err)
	}
	h.Restart(id) // stop the running version
	cur := filepath.Join(h.dir, id)
	prev := filepath.Join(h.dir, id+".previous")
	os.RemoveAll(prev)
	if _, err := os.Stat(cur); err == nil {
		if err := os.Rename(cur, prev); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, cur); err != nil {
		os.Rename(prev, cur)
		return err
	}
	h.mu.Lock()
	var v []string
	for _, x := range h.st.Verified {
		if x != id {
			v = append(v, x)
		}
	}
	if p.Verified {
		v = append(v, id)
	}
	h.st.Verified = v
	err := h.saveState()
	h.mu.Unlock()
	h.log.Info("add-on installed", "addon", id, "version", p.Manifest.Version, "verified", p.Verified)
	return err
}

func healthCheck(h *Host, dir string, m addon.Manifest) error {
	pr := &proc{id: m.ID + " (checking)", log: h.log, argv: []string{filepath.Join(dir, exeName())}, dir: dir, opens: map[string]addon.OpenParams{}}
	pr.stopping = true // never auto-restart the check
	if err := pr.start(); err != nil {
		return err
	}
	defer pr.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var res addon.InitializeResult
	if err := pr.call(ctx, "initialize", addon.InitializeParams{APIVersion: addon.APIVersion, Host: "ptz"}, &res); err != nil {
		return err
	}
	if res.Manifest.ID != m.ID || res.Manifest.Version != m.Version {
		return fmt.Errorf("program says it is %s %s, addon.json says %s %s", res.Manifest.ID, res.Manifest.Version, m.ID, m.Version)
	}
	return nil
}

// Remove deletes an installed version (a built-in add-on comes back).
func (h *Host) Remove(id string) error {
	if h.dir == "" || strings.ContainsAny(id, `/\.`) {
		return errors.New("bad add-on id")
	}
	h.Restart(id)
	if err := os.RemoveAll(filepath.Join(h.dir, id)); err != nil {
		return err
	}
	os.RemoveAll(filepath.Join(h.dir, id+".previous"))
	h.log.Info("add-on removed", "addon", id)
	return nil
}

// RollBack restores the version kept from the last install.
func (h *Host) RollBack(id string) error {
	prev := filepath.Join(h.dir, id+".previous")
	if _, err := os.Stat(prev); err != nil {
		return errors.New("there is no previous version to go back to")
	}
	h.Restart(id)
	cur := filepath.Join(h.dir, id)
	os.RemoveAll(cur + ".bad")
	os.Rename(cur, cur+".bad")
	if err := os.Rename(prev, cur); err != nil {
		os.Rename(cur+".bad", cur)
		return err
	}
	os.RemoveAll(cur + ".bad")
	h.log.Info("add-on rolled back", "addon", id)
	return nil
}

// HasPrevious reports whether Roll back is possible.
func (h *Host) HasPrevious(id string) bool {
	if h.dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(h.dir, id+".previous"))
	return err == nil
}
