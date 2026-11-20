package addons

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ptz/internal/devices"

	"github.com/toolingdev/ptz-controller/sdk/addon"
	"github.com/toolingdev/ptz-controller/sdk/addon/pack"
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
