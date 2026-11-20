// Command ptz-addon builds and checks jadeck add-ons.
//
//	ptz-addon new <id> [--name "Display name"] [--dir path]   start a new add-on project
//	ptz-addon check [dir]                                     validate the manifest
//	ptz-addon build [dir] [--key KEY] [--platforms ...]       build every platform + .ptzaddon
//	ptz-addon keygen <file>                                   make a signing key (for your own add-ons)
//
// KEY is a base64 ed25519 seed or a file holding one (default: the
// PTZ_SIGNING_KEY environment variable). Unsigned packages install with
// a "not verified" warning.
//
// Run it without installing:
//
//	go run github.com/toolingdev/jadeck/sdk/addon/cmd/ptz-addon build
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/toolingdev/jadeck/sdk/addon"
	"github.com/toolingdev/jadeck/sdk/addon/pack"
)

var allPlatforms = "windows/amd64,windows/arm64,darwin/arm64,darwin/amd64,linux/amd64,linux/arm64"

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "ptz-addon: "+f+"\n", a...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ptz-addon new|check|build|keygen ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "new":
		cmdNew(os.Args[2:])
	case "check":
		cmdCheck(os.Args[2:])
	case "build":
		cmdBuild(os.Args[2:])
	case "keygen":
		if len(os.Args) != 3 {
			die("usage: ptz-addon keygen <file>")
		}
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		if err := os.WriteFile(os.Args[2], []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
			die("%v", err)
		}
		fmt.Println("public key:", base64.StdEncoding.EncodeToString(pub))
	default:
		die("unknown command %q", os.Args[1])
	}
}

func readKey(arg string) ed25519.PrivateKey {
	if arg == "" {
		arg = os.Getenv("PTZ_SIGNING_KEY")
	}
	if arg == "" {
		return nil
	}
	s := arg
	if b, err := os.ReadFile(arg); err == nil {
		s = string(b)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != ed25519.SeedSize {
		die("signing key must be a %d-byte base64 seed (file or literal)", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(raw)
}

func run(dir string, env []string, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		die("%s %s: %v", name, strings.Join(args, " "), err)
	}
}

// manifestFrom runs the add-on for this platform with --manifest.
func manifestFrom(bin string) addon.Manifest {
	out, err := exec.Command(bin, "--manifest").Output()
	if err != nil {
		die("%s --manifest: %v", bin, err)
	}
	var m addon.Manifest
	if err := json.Unmarshal(out, &m); err != nil {
		die("--manifest printed something that isn't a manifest: %v", err)
	}
	if err := m.Validate(); err != nil {
		die("%v", err)
	}
	return m
}

func cmdBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	key := fs.String("key", "", "signing key (base64 seed or file); default $PTZ_SIGNING_KEY")
	plats := fs.String("platforms", allPlatforms, "comma-separated goos/goarch list")
	out := fs.String("out", "dist", "output folder for the .ptzaddon")
	fs.Parse(args)
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	priv := readKey(*key)
	os.RemoveAll(filepath.Join(dir, "bin"))
	host := runtime.GOOS + "/" + runtime.GOARCH
	var hostBin string
	for _, t := range strings.Split(*plats, ",") {
		t = strings.TrimSpace(t)
		goos, goarch, ok := strings.Cut(t, "/")
		if !ok {
			die("bad platform %q", t)
		}
		bin := filepath.Join("bin", goos+"-"+goarch, pack.ExeNameFor(goos))
		fmt.Println("building", t)
		run(dir, []string{"GOOS=" + goos, "GOARCH=" + goarch, "CGO_ENABLED=0"}, "go", "build", "-trimpath", "-ldflags", "-s -w", "-o", bin, ".")
		if t == host {
			hostBin = filepath.Join(dir, bin)
		}
	}
	if hostBin == "" {
		// The manifest comes from running the program, so also build for here.
		hostBin = filepath.Join(os.TempDir(), "ptz-addon-manifest-"+fmt.Sprint(os.Getpid())+pack.ExeNameFor(runtime.GOOS)[5:])
		run(dir, []string{"CGO_ENABLED=0"}, "go", "build", "-o", hostBin, ".")
		defer os.Remove(hostBin)
	}
	m := manifestFrom(hostBin)
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "addon.json"), append(b, '\n'), 0o644); err != nil {
		die("%v", err)
	}
	data, _, err := pack.Build(dir, priv)
	if err != nil {
		die("%v", err)
	}
	os.MkdirAll(filepath.Join(dir, *out), 0o755)
	file := filepath.Join(dir, *out, m.ID+".ptzaddon")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		die("%v", err)
	}
	signed := "unsigned (installs as \"not verified\")"
	if priv != nil {
		signed = "signed"
	}
	fmt.Printf("%s %s → %s (%d KB, %s)\n", m.Name, m.Version, file, len(data)>>10, signed)
}

func cmdCheck(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("ptz-addon-check-%d", os.Getpid()))
	run(dir, []string{"CGO_ENABLED=0"}, "go", "build", "-o", tmp, ".")
	defer os.Remove(tmp)
	m := manifestFrom(tmp)
	fmt.Printf("ok: %s %s — %d connection type(s), %d action(s), %d widget(s)\n", m.Name, m.Version,
		len(m.ConnectionTypes), len(m.Actions), len(m.Widgets))
}
