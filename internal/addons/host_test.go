package addons

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ptz/internal/devices"

	"github.com/toolingdev/jadeck/sdk/addon"
	"github.com/toolingdev/jadeck/sdk/addon/pack"
)

// buildCounter builds testdata/counter at a version into dir/bin/<platform>/.
func buildCounter(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", pack.Platform(), pack.ExeName())
	cmd := exec.Command("go", "build", "-ldflags", "-X main.Version="+version, "-o", bin, "./testdata/counter")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build test add-on: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "addon.json"), out, 0o644)
	os.MkdirAll(filepath.Join(dir, "ui"), 0o755)
	os.WriteFile(filepath.Join(dir, "ui", "panel.html"), []byte("<p>v"+version), 0o644)
	return dir
}

func TestHostRunsAddonProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	src := buildCounter(t, "1.0.0")
	data, _, err := pack.Build(src, priv)
	if err != nil {
		t.Fatal(err)
	}
	h := New(t.TempDir(), slog.New(slog.DiscardHandler))
	h.PubKey = pub
	defer h.Close()

	// Unsigned packages need consent; signed ones install as verified.
	unsigned, _, _ := pack.Build(src, nil)
	up, err := pack.Read(unsigned, pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Install(up, false); err == nil {
		t.Fatal("unsigned add-on installed without consent")
	}
	p, err := pack.Read(data, pub)
	if err != nil || !p.Verified {
		t.Fatalf("read: %v verified=%v", err, p != nil && p.Verified)
	}
	if err := h.Install(p, false); err != nil {
		t.Fatal(err)
	}
	if l := h.List(); len(l) != 1 || !l[0].Verified || l[0].Manifest.Version != "1.0.0" {
		t.Fatalf("list %+v", l)
	}

	// Open a connection through the add-on process and use it.
	set := devices.Open(&devices.File{Version: 2, Connections: []devices.Connection{{ID: "c1", Addon: "counter", Type: "sw", Name: "Counter"}}}, h.Open)
	if set.Switcher == nil {
		t.Fatalf("not opened: %v", set.Conns[0].Err)
	}
	ctx := context.Background()
	set.Switcher.Cut(ctx)
	st, err := set.Switcher.State(ctx)
	if err != nil || st.Active != 1 {
		t.Fatalf("state %+v %v", st, err)
	}

	// Resources come through the process; ui/ files were installed.
	impl := set.Conns[0].Impl
	rs := impl.(addon.ResultResourcer)
	if r, err := rs.ResourceResult(ctx, "pic.png?w=2"); err != nil || r.ContentType != "image/png" || string(r.Data) != "PNG!" || r.MaxAge != nil {
		t.Fatalf("resource %+v %v", r, err)
	}
	if _, err := rs.ResourceResult(ctx, "nope.png"); !errors.Is(err, addon.ErrNotFound) {
		t.Fatalf("missing resource: %v", err)
	}
	if r, err := rs.ResourceResult(ctx, "meters"); err != nil || r.MaxAge == nil || *r.MaxAge != 0 {
		t.Fatalf("meters %+v %v", r, err)
	}
	// v2.1: options for this connection (generic ones without it), state
	// for one widget.
	if o, err := impl.(addon.Optioner).AddonOptions(ctx, "inputs"); err != nil || len(o) != 1 || o[0].Label != "In 1 · One" {
		t.Fatalf("conn options %v %v", o, err)
	}
	if o := h.Options("counter", "inputs"); len(o) != 1 || o[0].Value != "g" {
		t.Fatalf("options %v", o)
	}
	st2, err := addon.Dispatch(ctx, impl, "addon.state", json.RawMessage(`{"settings":{"ch":["a","b"]}}`))
	if m, _ := st2.(map[string]any); err != nil || m["settings"] == nil {
		t.Fatalf("state for widget %v %v", st2, err)
	}
	st2, _ = addon.Dispatch(ctx, impl, "addon.state", nil)
	if m, _ := st2.(map[string]any); m["settings"] != nil || m["active"] == nil {
		t.Fatalf("plain state %v", st2)
	}
	if b, _, err := h.UIFile("counter", "panel.html"); err != nil || string(b) != "<p>v1.0.0" {
		t.Fatalf("ui file %q %v", b, err)
	}

	// It crashes: the host restarts it and reopens the connection.
	addon.Dispatch(ctx, set.Conns[0].Impl, "addon.action", json.RawMessage(`{"name":"crash"}`))
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err = set.Switcher.State(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("add-on did not come back: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if st.Active != 0 {
		t.Fatalf("restarted add-on should be fresh, active=%d", st.Active)
	}

	// Update to 1.1.0, then roll back.
	p2, _ := pack.Read(mustBuild(t, priv, "1.1.0"), pub)
	if err := h.Install(p2, false); err != nil {
		t.Fatal(err)
	}
	if m, _ := h.Manifest("counter"); m.Version != "1.1.0" {
		t.Fatalf("version after update %s", m.Version)
	}
	if err := h.RollBack("counter"); err != nil {
		t.Fatal(err)
	}
	if m, _ := h.Manifest("counter"); m.Version != "1.0.0" {
		t.Fatalf("version after roll back %s", m.Version)
	}

	// Turned off: connections say so.
	h.SetEnabled("counter", false)
	_, _, err = h.Open(devices.Connection{ID: "c2", Addon: "counter", Type: "sw"})
	if err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("open with add-on off: %v", err)
	}
}

func mustBuild(t *testing.T, priv ed25519.PrivateKey, v string) []byte {
	b, _, err := pack.Build(buildCounter(t, v), priv)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReadRejectsTampering(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	src := buildCounter(t, "1.0.0")
	// A program swapped after packing fails its SHA256SUMS.
	data, _, _ := pack.Build(src, priv)
	bad := append([]byte(nil), data...)
	i := strings.Index(string(bad), "addon.json")
	if i < 0 {
		t.Skip("layout")
	}
	if _, err := pack.Read(bad[:len(bad)-10], nil); err == nil {
		t.Fatal("truncated package accepted")
	}
	// Another key's signature reads but is not verified.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	p, err := pack.Read(data, other)
	if err != nil || p.Verified {
		t.Fatalf("other key: %v verified=%v", err, p != nil && p.Verified)
	}
}

// fakeInstalled writes an installed add-on folder (no real program: the
// manifest and files are all that's read until it runs).
func fakeInstalled(t *testing.T, dir string, manifest string, files map[string]string) {
	t.Helper()
	d := filepath.Join(dir, "x")
	os.MkdirAll(filepath.Join(d, "ui", "img"), 0o755)
	os.WriteFile(filepath.Join(d, "addon.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(d, exeName()), []byte("program"), 0o755)
	for n, b := range files {
		os.WriteFile(filepath.Join(d, filepath.FromSlash(n)), []byte(b), 0o644)
	}
}

const v2Manifest = `{"id":"x","name":"X","version":"1.0.0","apiVersion":2,
 "connectionTypes":[{"id":"s","label":"S","capabilities":["slides"],"fields":[]}],
 "actions":[{"id":"go","label":"Go"}],
 "widgets":[
  {"kind":"frame","type":"live","label":"Live","entry":"ui/live.html","sample":{}},
  {"kind":"buttons","type":"b","label":"B","bind":{"items":"btns","action":"go"},"sample":{}},
  {"kind":"hologram","type":"h","label":"H","sample":{}}]}`

func TestHostDropsUnknownWidgetKinds(t *testing.T) {
	var logs strings.Builder
	dir := t.TempDir()
	fakeInstalled(t, dir, v2Manifest, nil)
	h := New(dir, slog.New(slog.NewTextHandler(&logs, nil)))
	m, ok := h.Manifest("x")
	if !ok {
		t.Fatal("v2 add-on with an unknown widget kind was refused")
	}
	if len(m.Widgets) != 2 || m.Widgets[0].Kind != "frame" || m.Widgets[1].Kind != "buttons" {
		t.Fatalf("widgets %+v", m.Widgets)
	}
	h.Manifest("x")
	if n := strings.Count(logs.String(), "widget left out"); n != 1 {
		t.Fatalf("want one warning, got %d:\n%s", n, logs.String())
	}
	// apiVersion 3 is still refused.
	fakeInstalled(t, dir, strings.Replace(v2Manifest, `"apiVersion":2`, `"apiVersion":3`, 1), nil)
	if _, ok := h.Manifest("x"); ok {
		t.Fatal("apiVersion 3 accepted")
	}
}

func TestHostUIFiles(t *testing.T) {
	dir := t.TempDir()
	fakeInstalled(t, dir, v2Manifest, map[string]string{"ui/live.html": "<p>hi", "ui/img/a.png": "png", "ui/run.sh": "rm -rf", "secret.txt": "no"})
	os.Symlink(filepath.Join(dir, "x", "secret.txt"), filepath.Join(dir, "x", "ui", "link.js"))
	h := New(dir, slog.New(slog.DiscardHandler))
	if b, _, err := h.UIFile("x", "live.html"); err != nil || string(b) != "<p>hi" {
		t.Fatalf("live.html %q %v", b, err)
	}
	if b, _, err := h.UIFile("x", "img/a.png"); err != nil || string(b) != "png" {
		t.Fatalf("img/a.png %q %v", b, err)
	}
	for _, bad := range []string{"../secret.txt", "../addon.json", "img", "", ".", "run.sh", "/live.html", "img/../live.html", "link.js", "missing.js"} {
		if _, _, err := h.UIFile("x", bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, _, err := h.UIFile("nope", "live.html"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown add-on: %v", err)
	}
	h.SetEnabled("x", false)
	if _, _, err := h.UIFile("x", "live.html"); !errors.Is(err, ErrNotFound) {
		t.Errorf("turned-off add-on served ui: %v", err)
	}
}
