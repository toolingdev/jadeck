package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// AssetName is this build's release asset.
func AssetName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("ptz-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

// Status is GET /api/update/status. state is one of
// none | checking | available | invalid | applying | applied | rolled_back | failed | disabled
type Status struct {
	State     string `json:"state"`
	Current   string `json:"current"`
	Version   string `json:"version,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Reason    string `json:"reason,omitempty"`
	LastCheck string `json:"lastCheck,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

// HelperState is what the swap helper persists for the next start.
type HelperState struct {
	State   string `json:"state"` // applying | applied | rolled_back | failed
	Version string `json:"version"`
	From    string `json:"from"`
	Reason  string `json:"reason,omitempty"`
	At      string `json:"at"`
}

type staged struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Notes   string `json:"notes"`
	File    string `json:"file"`
}

type Manager struct {
	Current string
	Source  *Source
	PubKey  ed25519.PublicKey
	Dir     string // <data>/updates
	Every   time.Duration
	Log     *slog.Logger

	kick chan struct{} // Settings changed: check now

	mu        sync.Mutex
	checking  bool
	invalid   string
	checkErr  string
	lastCheck time.Time
	stage     *staged
}

func (m *Manager) stagedPath() string { return filepath.Join(m.Dir, "staged.json") }

// blockVersion records a release that failed to apply; it is never
// offered again (a newer release is).
func blockVersion(dir, v string) {
	l := blocked(dir)
	for _, x := range l {
		if x == v {
			return
		}
	}
	b, _ := json.Marshal(append(l, v))
	writeAtomic(filepath.Join(dir, "blocked.json"), b, 0o644)
}

func blocked(dir string) []string {
	var l []string
	if b, err := os.ReadFile(filepath.Join(dir, "blocked.json")); err == nil {
		json.Unmarshal(b, &l)
	}
	return l
}

func (m *Manager) isBlocked(v string) bool {
	for _, x := range blocked(m.Dir) {
		if x == v {
			return true
		}
	}
	return false
}
func (m *Manager) StatePath() string { return filepath.Join(m.Dir, "state.json") }

// SetSource swaps where releases come from (Settings → System).
func (m *Manager) SetSource(src *Source) {
	m.mu.Lock()
	changed := m.Source == nil || *m.Source != *src
	m.Source = src
	k := m.kick
	m.mu.Unlock()
	if changed && k != nil {
		select {
		case k <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) source() *Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Source
}

// Enabled is false when no repo or no signing key is configured.
func (m *Manager) Enabled() (bool, string) { return m.enabledFor(m.source()) }

func (m *Manager) enabledFor(src *Source) (bool, string) {
	switch {
	case src == nil || src.Repo == "":
		return false, "no release source set (Settings → System → Updates)"
	case m.PubKey == nil:
		return false, "no release signing key in this build (PTZ_UPDATE_PUBKEY) — unsigned updates are refused"
	}
	return true, ""
}

// Run checks at start and then every m.Every until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	m.loadStaged()
	m.mu.Lock()
	if m.kick == nil {
		m.kick = make(chan struct{}, 1)
	}
	kick := m.kick
	m.mu.Unlock()
	every := m.Every
	if every == 0 {
		every = time.Hour
	}
	for {
		if ok, _ := m.Enabled(); ok {
			if err := m.Check(ctx); err != nil && ctx.Err() == nil {
				m.Log.Info("update check", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-time.After(every):
		}
	}
}

func (m *Manager) loadStaged() {
	b, err := os.ReadFile(m.stagedPath())
	if err != nil {
		return
	}
	var s staged
	if json.Unmarshal(b, &s) == nil && Newer(s.Version, m.Current) && !m.isBlocked(s.Version) {
		m.mu.Lock()
		m.stage = &s
		m.mu.Unlock()
	}
}

// Check looks for a newer release and, if found, downloads and fully
// verifies it before offering it.
func (m *Manager) Check(ctx context.Context) error {
	if ok, why := m.Enabled(); !ok {
		return errors.New(why)
	}
	m.mu.Lock()
	if m.checking {
		m.mu.Unlock()
		return nil
	}
	m.checking = true
	m.mu.Unlock()
	err := m.check(ctx)
	m.mu.Lock()
	m.checking = false
	m.lastCheck = time.Now()
	m.checkErr = ""
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			m.invalid = err.Error()
		} else {
			m.checkErr = err.Error()
		}
	}
	m.mu.Unlock()
	return err
}

func (m *Manager) check(ctx context.Context) error {
	src := m.source()
	rel, err := src.Latest(ctx)
	if err != nil {
		return err
	}
	if !Newer(rel.Tag, m.Current) || m.isBlocked(rel.Tag) {
		return nil
	}
	m.mu.Lock()
	if m.stage != nil && m.stage.Version == rel.Tag {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	find := func(name string) (Asset, bool) {
		for _, a := range rel.Assets {
			if a.Name == name {
				return a, true
			}
		}
		return Asset{}, false
	}
	binA, ok1 := find(AssetName())
	sumA, ok2 := find("SHA256SUMS")
	sigA, ok3 := find("SHA256SUMS.sig")
	if !ok1 || !ok2 || !ok3 {
		return fmt.Errorf("%w: release %s is missing %s, SHA256SUMS or SHA256SUMS.sig", ErrInvalid, rel.Tag, AssetName())
	}
	sums, err := src.Download(ctx, sumA, 1<<20)
	if err != nil {
		return err
	}
	sig, err := src.Download(ctx, sigA, 4096)
	if err != nil {
		return err
	}
	want, err := VerifySums(m.PubKey, sums, sig, AssetName())
	if err != nil {
		return err
	}
	bin, err := src.Download(ctx, binA, 256<<20)
	if err != nil {
		return err
	}
	if err := VerifyBinary(bin, want); err != nil {
		return err
	}
	dir := filepath.Join(m.Dir, "staged")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, AssetName())
	if err := writeAtomic(file, bin, 0o755); err != nil {
		return err
	}
	s := staged{Version: rel.Tag, SHA256: want, Notes: rel.Body, File: file}
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := writeAtomic(m.stagedPath(), b, 0o644); err != nil {
		return err
	}
	m.mu.Lock()
	m.stage, m.invalid = &s, ""
	m.mu.Unlock()
	m.Log.Info("update staged and verified", "version", rel.Tag)
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) helperState() *HelperState {
	b, err := os.ReadFile(m.StatePath())
	if err != nil {
		return nil
	}
	var h HelperState
	if json.Unmarshal(b, &h) != nil {
		return nil
	}
	return &h
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{State: "none", Current: m.Current}
	if m.Source != nil {
		st.Repo = m.Source.Repo
	}
	if !m.lastCheck.IsZero() {
		st.LastCheck = m.lastCheck.Format(time.RFC3339)
	}
	if ok, why := m.enabledFor(m.Source); !ok {
		st.State, st.Reason = "disabled", why
		return st
	}
	if h := m.helperState(); h != nil {
		// Report the outcome of the last apply to the build now running.
		show := false
		switch h.State {
		case "applying":
			show = true
		case "applied":
			show = h.Version == m.Current // we are the new build
		case "rolled_back", "failed":
			show = h.From == m.Current // we are the build that came back
		}
		if show {
			st.State, st.Version, st.Reason = h.State, h.Version, h.Reason
		}
	}
	if st.State == "rolled_back" || st.State == "failed" {
		return st // the operator sees why before anything else is offered
	}
	switch {
	case m.stage != nil && Newer(m.stage.Version, m.Current) && !m.isBlocked(m.stage.Version):
		st.State, st.Version, st.Notes, st.Reason = "available", m.stage.Version, m.stage.Notes, ""
	case m.invalid != "":
		st.State, st.Reason = "invalid", m.invalid
	case m.checking:
		st.State = "checking"
	case st.State == "none" && m.checkErr != "":
		st.Reason = m.checkErr
	}
	return st
}

// DismissOutcome clears the applied / rolled_back banner.
func (m *Manager) DismissOutcome() { os.Remove(m.StatePath()) }

// ApplyRequest is how the running process hands off to the helper.
type ApplyRequest struct {
	Mode       string   // "service" or "exec"
	HealthURL  string   // e.g. http://127.0.0.1:8080/api/version
	ExecArgs   []string // exec mode: args to restart with
	ServiceArg []string // service mode: service arguments
}

// Apply re-verifies the staged binary and launches the swap helper
// detached. In exec mode the caller must exit afterwards.
func (m *Manager) Apply(req ApplyRequest) (string, error) {
	m.mu.Lock()
	s := m.stage
	m.mu.Unlock()
	if s == nil {
		return "", errors.New("no verified update is staged")
	}
	data, err := os.ReadFile(s.File)
	if err != nil {
		return "", err
	}
	if err := VerifyBinary(data, s.SHA256); err != nil { // second verification
		m.mu.Lock()
		m.stage, m.invalid = nil, err.Error()
		m.mu.Unlock()
		os.Remove(m.stagedPath())
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	hdir := filepath.Join(m.Dir, "helper")
	os.MkdirAll(hdir, 0o755)
	helper := filepath.Join(hdir, filepath.Base(exe))
	if err := copyFile(exe, helper, 0o755); err != nil {
		return "", fmt.Errorf("copy helper: %w", err)
	}
	argsJSON, _ := json.Marshal(req.ExecArgs)
	svcJSON, _ := json.Marshal(req.ServiceArg)
	hs := HelperState{State: "applying", Version: s.Version, From: m.Current, At: time.Now().Format(time.RFC3339)}
	b, _ := json.Marshal(hs)
	writeAtomic(m.StatePath(), b, 0o644)
	logf, _ := os.OpenFile(filepath.Join(m.Dir, "helper.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	cmd := exec.Command(helper, "_apply",
		"--exe", exe, "--new", s.File, "--sha", s.SHA256, "--version", s.Version, "--from", m.Current,
		"--health", req.HealthURL, "--state", m.StatePath(), "--mode", req.Mode,
		"--exec-args", string(argsJSON), "--service-args", string(svcJSON))
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Dir = hdir
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("launch helper: %w", err)
	}
	cmd.Process.Release()
	m.Log.Info("applying update — restarting", "from", m.Current, "to", s.Version, "mode", req.Mode, "helperLog", filepath.Join(m.Dir, "helper.log"))
	return s.Version, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	os.Remove(dst)
	return os.Rename(tmp, dst)
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
