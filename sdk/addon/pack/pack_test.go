package pack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/toolingdev/jadeck/sdk/addon"
)

func manifest() addon.Manifest {
	return addon.Manifest{ID: "t", Name: "T", Version: "1.0.0", APIVersion: 2,
		ConnectionTypes: []addon.ConnectionType{{ID: "s", Label: "S", Capabilities: []string{addon.CapSlides}}},
		Widgets:         []addon.WidgetDef{{Kind: "frame", Type: "live", Label: "Live", Entry: "ui/live.html", Sample: map[string]any{}}}}
}

// project writes an add-on folder: addon.json, a fake program and files.
func project(t *testing.T, m addon.Manifest, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	b, _ := json.Marshal(m)
	write := func(name, body string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("addon.json", string(b))
	write("bin/"+Platform()+"/"+ExeName(), "program")
	for n, body := range files {
		write(n, body)
	}
	return dir
}

func TestBuildIncludesUI(t *testing.T) {
	dir := project(t, manifest(), map[string]string{
		"ui/live.html": "<!doctype html><script src=app.js></script>", "ui/app.js": "1", "ui/img/logo.png": "png",
		"ui/.DS_Store": "junk", "notes.txt": "not packed",
	})
	data, _, err := Build(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Read(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	ui := p.UI()
	if len(ui) != 3 || string(ui["ui/app.js"]) != "1" || ui["ui/img/logo.png"] == nil {
		t.Fatalf("ui files %v", keys(ui))
	}
	if !strings.Contains(string(p.Files["SHA256SUMS"]), "  ui/live.html\n") {
		t.Fatal("ui file not in SHA256SUMS")
	}
	if _, ok := p.Files["notes.txt"]; ok {
		t.Fatal("stray file packed")
	}
}

func TestBuildRejectsBadUI(t *testing.T) {
	if _, _, err := Build(project(t, manifest(), map[string]string{"ui/live.html": "x", "ui/run.exe": "x"}), nil); err == nil || !strings.Contains(err.Error(), "run.exe") {
		t.Fatalf("exe in ui/: %v", err)
	}
	big := strings.Repeat("x", addon.MaxUISize)
	if _, _, err := Build(project(t, manifest(), map[string]string{"ui/live.html": "x", "ui/big.js": big}), nil); err == nil || !strings.Contains(err.Error(), "most") {
		t.Fatalf("over 2 MB: %v", err)
	}
	if _, _, err := Build(project(t, manifest(), map[string]string{"ui/other.html": "x"}), nil); err == nil || !strings.Contains(err.Error(), "ui/live.html") {
		t.Fatalf("missing entry: %v", err)
	}
	// A v1 add-on without ui/ builds as before.
	m := manifest()
	m.APIVersion, m.Widgets = 1, nil
	if _, _, err := Build(project(t, m, nil), nil); err != nil {
		t.Fatal(err)
	}
}

// zipOf builds a package by hand (with correct sums), to feed Read
// things Build refuses to make.
func zipOf(t *testing.T, manifestJSON []byte, files map[string]string) []byte {
	t.Helper()
	all := map[string][]byte{"addon.json": manifestJSON, "bin/" + Platform() + "/" + ExeName(): []byte("program")}
	for n, b := range files {
		all[n] = []byte(b)
	}
	names := keys(all)
	var sums strings.Builder
	for _, n := range names {
		s := sha256.Sum256(all[n])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(s[:]), n)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, _ := zw.Create(n)
		w.Write(all[n])
	}
	w, _ := zw.Create("SHA256SUMS")
	w.Write([]byte(sums.String()))
	zw.Close()
	return buf.Bytes()
}

func TestReadChecksUI(t *testing.T) {
	mb, _ := json.Marshal(manifest())
	if _, err := Read(zipOf(t, mb, map[string]string{"ui/live.html": "x"}), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(zipOf(t, mb, map[string]string{"ui/live.html": "x", "ui/evil.sh": "x"}), nil); err == nil || !strings.Contains(err.Error(), "evil.sh") {
		t.Fatalf("bad ext: %v", err)
	}
	if _, err := Read(zipOf(t, mb, map[string]string{"ui/live.html": strings.Repeat("x", addon.MaxUISize+1)}), nil); err == nil {
		t.Fatal("over 2 MB read")
	}
	if _, err := Read(zipOf(t, mb, nil), nil); err == nil || !strings.Contains(err.Error(), "not in the package") {
		t.Fatalf("missing entry: %v", err)
	}
}

func TestReadDropsUnknownWidgetKinds(t *testing.T) {
	m := manifest()
	m.Widgets = append(m.Widgets, addon.WidgetDef{Kind: "hologram", Type: "h", Label: "H", Sample: map[string]any{}})
	mb, _ := json.Marshal(m)
	p, err := Read(zipOf(t, mb, map[string]string{"ui/live.html": "x"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Dropped) != 1 || p.Dropped[0].Type != "h" || len(p.Manifest.Widgets) != 1 {
		t.Fatalf("dropped %v widgets %v", p.Dropped, p.Manifest.Widgets)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
