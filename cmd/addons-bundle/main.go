// Command addons-bundle fills internal/addons/bundle with the standard
// add-ons' .ptzaddon packages, for `go build -tags addonbundle`.
//
//	go run ./cmd/addons-bundle                  latest GitHub release of each add-on repo
//	go run ./cmd/addons-bundle --local ../ptz-addons [--key KEY]   build from local projects
//
// Each add-on is its own project (github.com/toolingdev/jadeck-addon-<id>);
// this only collects their released packages.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Standard add-ons shipped with every release.
var standard = []string{"vmix", "behringer-wing", "proclaim", "midi", "http-api", "simulated"}

const out = "internal/addons/bundle"

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "addons-bundle: "+f+"\n", a...)
	os.Exit(1)
}

func main() {
	local := flag.String("local", "", "folder holding the add-on projects (build them instead of downloading)")
	key := flag.String("key", "", "signing key for --local builds (base64 seed or file; default $PTZ_SIGNING_KEY)")
	owner := flag.String("owner", "toolingdev", "GitHub owner of the jadeck-addon-<id> repositories")
	platform := flag.String("platform", runtime.GOOS+"/"+runtime.GOARCH, "keep only this goos/goarch program in each package (the signature stays valid)")
	flag.Parse()
	plat := strings.ReplaceAll(*platform, "/", "-")
	os.MkdirAll(out, 0o755)
	old, _ := filepath.Glob(filepath.Join(out, "*.ptzaddon"))
	for _, f := range old {
		os.Remove(f)
	}
	for _, id := range standard {
		dst := filepath.Join(out, id+".ptzaddon")
		if *local != "" {
			dir := filepath.Join(*local, id)
			args := []string{"run", "github.com/toolingdev/jadeck/sdk/addon/cmd/ptz-addon", "build"}
			if *key != "" {
				args = append(args, "--key", *key)
			}
			cmd := exec.Command("go", args...)
			cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				die("%s: %v", id, err)
			}
			b, err := os.ReadFile(filepath.Join(dir, "dist", id+".ptzaddon"))
			if err != nil {
				die("%v", err)
			}
			if err := writeStripped(dst, b, plat); err != nil {
				die("%s: %v", id, err)
			}
			continue
		}
		if err := download(*owner, id, dst, plat); err != nil {
			die("%s: %v", id, err)
		}
	}
	fmt.Println("bundle ready:", strings.Join(standard, ", "))
}

// writeStripped keeps only one platform's program. SHA256SUMS (and its
// signature) stay as they are: every file left is still listed in them.
func writeStripped(dst string, data []byte, plat string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	kept := false
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "bin/") && !strings.HasPrefix(f.Name, "bin/"+plat+"/") {
			continue
		}
		kept = kept || strings.HasPrefix(f.Name, "bin/"+plat+"/")
		if err := zw.Copy(f); err != nil {
			return err
		}
	}
	if !kept {
		return fmt.Errorf("package has no program for %s", plat)
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}

func download(owner, id, dst, plat string) error {
	cl := &http.Client{Timeout: 60 * time.Second}
	get := func(url, accept string) (*http.Response, error) {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Accept", accept)
		if t := os.Getenv("GITHUB_TOKEN"); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		return cl.Do(req)
	}
	resp, err := get(fmt.Sprintf("https://api.github.com/repos/%s/jadeck-addon-%s/releases/latest", owner, id), "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("no release (HTTP %d)", resp.StatusCode)
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return err
	}
	for _, a := range rel.Assets {
		if a.Name != id+".ptzaddon" {
			continue
		}
		r, err := get(a.URL, "application/octet-stream")
		if err != nil {
			return err
		}
		defer r.Body.Close()
		b, err := io.ReadAll(io.LimitReader(r.Body, 200<<20))
		if err != nil {
			return err
		}
		fmt.Printf("%s %s (%d KB)\n", id, rel.Tag, len(b)>>10)
		return writeStripped(dst, b, plat)
	}
	return fmt.Errorf("release %s has no %s.ptzaddon", rel.Tag, id)
}
