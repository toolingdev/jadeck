// Package pack reads and writes .ptzaddon files, the way add-ons are
// shipped. Add-on projects build them with the ptz-addon tool
// (go run github.com/toolingdev/jadeck/sdk/addon/cmd/ptz-addon build);
// the controller reads them when installing or updating an add-on.
package pack

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// A .ptzaddon file is a zip:
//
//	addon.json                      the manifest
//	bin/<goos>-<goarch>/addon[.exe] the program, one per platform it supports
//	README.md, icon.svg             optional
//	ui/...                          optional (apiVersion 2): files for frame widgets;
//	                                html/js/css/svg/png/woff2/json, at most 2 MB in all
//	SHA256SUMS                      sha256 of every other file ("<hex>  <path>")
//	SHA256SUMS.sig                  optional: base64 ed25519 signature of SHA256SUMS
//
// A signature by the controller's release key makes it "verified".
// Unsigned (or signed by someone else) add-ons install only when the
// person installing says so.

// Platform is this controller's bin/ folder name.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// ErrUnverified: the package isn't signed by the release key.
var ErrUnverified = errors.New("this add-on is not signed by the jadeck release key")

// Package is a read and checked .ptzaddon.
type Package struct {
	Manifest addon.Manifest
	Verified bool
	Program  []byte            // the program for this platform
	Files    map[string][]byte // every file in the package (ui/ files under "ui/...")
	// Dropped are widgets whose kind this controller doesn't know; they
	// are not in Manifest.Widgets (addon.json in Files still has them).
	Dropped []addon.WidgetDef
}

// UI returns the package's ui/ files (path → content).
func (p *Package) UI() map[string][]byte {
	out := map[string][]byte{}
	for n, b := range p.Files {
		if strings.HasPrefix(n, "ui/") {
			out[n] = b
		}
	}
	return out
}

// checkUI checks the ui/ folder: allowed file types only, at most
// addon.MaxUISize in all, and every frame widget's entry present.
func checkUI(files map[string][]byte, m addon.Manifest) error {
	var total int
	for n, b := range files {
		if !strings.HasPrefix(n, "ui/") {
			continue
		}
		if !addon.UIFileAllowed(n) {
			return fmt.Errorf("add-on package: ui file %s is not allowed (only %s)", n, strings.Join(addon.UIExtensions, " "))
		}
		total += len(b)
	}
	if total > addon.MaxUISize {
		return fmt.Errorf("add-on package: ui/ holds %d KB; the most is %d KB", total>>10, addon.MaxUISize>>10)
	}
	for _, w := range m.Widgets {
		if w.Kind == addon.KindFrame {
			if _, ok := files[w.Entry]; !ok {
				return fmt.Errorf("add-on package: widget %s loads %s, which is not in the package", w.Type, w.Entry)
			}
		}
	}
	return nil
}

const maxPackage = 200 << 20

// Read unzips and checks a package in memory.
func Read(data []byte, pub ed25519.PublicKey) (*Package, error) {
	if len(data) > maxPackage {
		return nil, fmt.Errorf("add-on package is too big (%d MB)", len(data)>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not an add-on package (.ptzaddon is a zip): %w", err)
	}
	files := map[string][]byte{}
	var total int64
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return nil, fmt.Errorf("add-on package has an unsafe path %q", f.Name)
		}
		total += int64(f.UncompressedSize64)
		if total > 2*maxPackage {
			return nil, errors.New("add-on package expands too big")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxPackage))
		rc.Close()
		if err != nil {
			return nil, err
		}
		files[name] = b
	}
	mb, ok := files["addon.json"]
	if !ok {
		return nil, errors.New("add-on package has no addon.json")
	}
	var m addon.Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, fmt.Errorf("addon.json: %w", err)
	}
	// Widgets of kinds this controller doesn't know are left out (see
	// Package.Dropped), so an add-on made for a newer controller installs.
	dropped := m.DropUnknownWidgets()
	if err := m.Validate(); err != nil {
		return nil, err
	}
	sums, ok := files["SHA256SUMS"]
	if !ok {
		return nil, errors.New("add-on package has no SHA256SUMS (build it with `ptz addon pack`)")
	}
	listed := map[string]string{}
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			listed[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	for name, b := range files {
		if name == "SHA256SUMS" || name == "SHA256SUMS.sig" {
			continue
		}
		want, ok := listed[name]
		got := sha256.Sum256(b)
		if !ok || hex.EncodeToString(got[:]) != want {
			return nil, fmt.Errorf("add-on package file %s does not match SHA256SUMS", name)
		}
	}
	if err := checkUI(files, m); err != nil {
		return nil, err
	}
	p := &Package{Manifest: m, Files: files, Dropped: dropped}
	if sig, ok := files["SHA256SUMS.sig"]; ok && len(pub) == ed25519.PublicKeySize {
		if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig))); err == nil && ed25519.Verify(pub, sums, raw) {
			p.Verified = true
		}
	}
	prog := "bin/" + Platform() + "/" + ExeName()
	if p.Program, ok = files[prog]; !ok {
		var have []string
		for n := range files {
			if strings.HasPrefix(n, "bin/") {
				have = append(have, strings.Split(n, "/")[1])
			}
		}
		return nil, fmt.Errorf("this add-on has no program for %s (it has: %s)", Platform(), strings.Join(have, ", "))
	}
	return p, nil
}

// Build builds a .ptzaddon from a folder holding addon.json and
// bin/<goos>-<goarch>/addon[.exe] (and optional README.md, icon.svg,
// CHANGELOG.md, ui/). priv signs it (nil = unsigned).
func Build(dir string, priv ed25519.PrivateKey) ([]byte, addon.Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return nil, m, err
	}
	files := map[string][]byte{}
	mb, _ := os.ReadFile(filepath.Join(dir, "addon.json"))
	files["addon.json"] = mb
	bins, _ := filepath.Glob(filepath.Join(dir, "bin", "*", "addon*"))
	if len(bins) == 0 {
		return nil, m, errors.New("no programs under bin/<goos>-<goarch>/addon[.exe]")
	}
	for _, b := range bins {
		rel, _ := filepath.Rel(dir, b)
		data, err := os.ReadFile(b)
		if err != nil {
			return nil, m, err
		}
		files[filepath.ToSlash(rel)] = data
	}
	for _, extra := range []string{"README.md", "icon.svg", "CHANGELOG.md"} {
		if b, err := os.ReadFile(filepath.Join(dir, extra)); err == nil {
			files[extra] = b
		}
	}
	if err := readUI(dir, files); err != nil {
		return nil, m, err
	}
	if err := checkUI(files, m); err != nil {
		return nil, m, err
	}
	var sums strings.Builder
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), n)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, b []byte, mode os.FileMode) error {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}
	for _, n := range names {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(n, "bin/") {
			mode = 0o755
		}
		if err := add(n, files[n], mode); err != nil {
			return nil, m, err
		}
	}
	add("SHA256SUMS", []byte(sums.String()), 0o644)
	if priv != nil {
		add("SHA256SUMS.sig", []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums.String())))), 0o644)
	}
	if err := zw.Close(); err != nil {
		return nil, m, err
	}
	return buf.Bytes(), m, nil
}

// readUI adds dir/ui/** to files as "ui/...". Hidden files (.DS_Store)
// are skipped; anything else not allowed is an error.
func readUI(dir string, files map[string][]byte) error {
	root := filepath.Join(dir, "ui")
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && p != root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		name := filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: ui/ may hold only plain files", name)
		}
		if !addon.UIFileAllowed(name) {
			return fmt.Errorf("%s: ui/ may hold only %s files", name, strings.Join(addon.UIExtensions, " "))
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[name] = b
		return nil
	})
}

// ExeName is the program's file name on this platform.
func ExeName() string { return ExeNameFor(runtime.GOOS) }

// ExeNameFor is the program's file name on goos.
func ExeNameFor(goos string) string {
	if goos == "windows" {
		return "addon.exe"
	}
	return "addon"
}

// ReadManifest reads and validates dir/addon.json.
func ReadManifest(dir string) (addon.Manifest, error) {
	var m addon.Manifest
	b, err := os.ReadFile(filepath.Join(dir, "addon.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, m.Validate()
}
