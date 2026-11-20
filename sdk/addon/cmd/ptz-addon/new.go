package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// SDKVersion is the SDK release new projects require.
const SDKVersion = "v0.1.0"

var files = map[string]string{
	"go.mod": `module {{.Module}}

go 1.24

require github.com/toolingdev/ptz-controller/sdk/addon {{.SDK}}
`,
	".gitignore": "bin/\ndist/\naddon.json\n",
	"main.go": `// {{.Name}}: a PTZ controller add-on.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/toolingdev/ptz-controller/sdk/addon"
)

// Version is set by the release build (-ldflags "-X main.Version=1.2.3").
var Version = "0.1.0"

func main() { addon.Serve(spec()) }

func spec() addon.Spec {
	return addon.Spec{
		Manifest: addon.Manifest{
			ID: "{{.ID}}", Name: "{{.Name}}", Version: Version, Publisher: "{{.Publisher}}",
			APIVersion:  addon.APIVersion,
			Description: "What it does, in one line (100 characters at most).",
			Reaches:     "What it talks to, e.g. the {{.Name}} PCs you add as connections",
			ConnectionTypes: []addon.ConnectionType{{"{{"}}
				ID: "{{.ID}}", Label: "{{.Name}}", Capabilities: []string{addon.CapSlides},
				Description: "Where to turn this on in {{.Name}}.",
				Fields: []addon.Field{
					{Key: "host", Label: "Address", Type: "text", Default: "127.0.0.1", Required: true},
				},
				Sample:     map[string]any{"host": "192.168.1.20"},
				SampleTest: "Connected",
			}},
			// Widgets: previews for Settings → Add-ons. Built-in widgets
			// ("core") work with any connection that has the capability.
			Widgets: []addon.WidgetDef{
				{Kind: "core", Type: "slides.prevnext", Label: "Prev / Next", Sample: map[string]any{
					"/api/presentation/status": map[string]any{"available": true, "actions": []string{"prev", "next"}}}},
			},
		},
		Open: map[string]addon.OpenFunc{"{{.ID}}": open},
	}
}

type conn struct{ host string }

func open(raw json.RawMessage) (any, error) {
	var c struct {
		Host string ` + "`json:\"host\"`" + `
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c.Host == "" {
		return nil, fmt.Errorf("enter the address: %w", addon.ErrNotConfigured)
	}
	return &conn{host: c.Host}, nil
}

// addon.Presentation (the "slides" capability)
func (c *conn) Info() addon.PresentationInfo {
	return addon.PresentationInfo{Available: true, Target: c.host}
}
func (c *conn) Actions() []string { return []string{"prev", "next"} }
func (c *conn) Do(ctx context.Context, action string) (map[string]any, error) {
	switch action {
	case "prev", "next":
		// TODO: tell {{.Name}} to change slide.
		return map[string]any{"action": action}, nil
	}
	return nil, fmt.Errorf("%w %q", addon.ErrUnknownAction, action)
}
`,
	"main_test.go": `package main

import (
	"testing"

	"github.com/toolingdev/ptz-controller/sdk/addon/addontest"
)

func TestAddon(t *testing.T) {
	h := addontest.Start(t, spec())
	h.Open("c1", "{{.ID}}", map[string]any{"host": "127.0.0.1"})
	var d map[string]any
	h.Call("c1", "slides.do", map[string]any{"action": "next"}, &d)
	if d["action"] != "next" {
		t.Fatalf("got %v", d)
	}
}
`,
	"README.md": "# {{.Name}} — PTZ controller add-on\n\nBuild every platform and the `.ptzaddon` package:\n\n```bash\ngo run github.com/toolingdev/ptz-controller/sdk/addon/cmd/ptz-addon build\n```\n\nTry it on a controller: Settings → Add-ons → Developer → Load a folder (this folder),\nor install `dist/{{.ID}}.ptzaddon` with Install from file.\n\nRelease: push a tag `v1.2.3`; the workflow builds, signs (secret `PTZ_SIGNING_KEY`)\nand attaches `{{.ID}}.ptzaddon` to the GitHub release.\n",
	".github/workflows/release.yml": `name: release
on:
  push:
    branches: [main]
    tags: ["v*"]
  pull_request:
permissions:
  contents: read
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - run: go vet ./... && go test -race ./...
  release:
    if: startsWith(github.ref, 'refs/tags/v')
    needs: test
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - name: Build, pack and sign
        env:
          PTZ_SIGNING_KEY: ${{"{{"}} secrets.PTZ_SIGNING_KEY {{"}}"}}
        run: |
          v="${GITHUB_REF_NAME#v}"
          sed -i "s/^var Version = .*/var Version = \"$v\"/" main.go
          go run github.com/toolingdev/ptz-controller/sdk/addon/cmd/ptz-addon build
      - name: Publish
        env:
          GH_TOKEN: ${{"{{"}} github.token {{"}}"}}
        run: gh release create "$GITHUB_REF_NAME" dist/*.ptzaddon --generate-notes
`,
}

var newID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

func cmdNew(args []string) {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	name := fs.String("name", "", "display name (≤ 40 characters)")
	dir := fs.String("dir", "", "folder (default ./<id>)")
	module := fs.String("module", "", "Go module path (default github.com/you/ptz-addon-<id>)")
	pub := fs.String("publisher", "", "publisher name")
	// flags may come after the id
	var id string
	rest := args
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		id, rest = rest[0], rest[1:]
	}
	fs.Parse(rest)
	if id == "" && fs.NArg() > 0 {
		id = fs.Arg(0)
	}
	if !newID.MatchString(id) {
		die("usage: ptz-addon new <id> (a-z, 0-9 and -)")
	}
	if *name == "" {
		*name = strings.ToUpper(id[:1]) + id[1:]
	}
	if *dir == "" {
		*dir = id
	}
	if *module == "" {
		*module = "github.com/you/ptz-addon-" + id
	}
	if *pub == "" {
		*pub = "You"
	}
	data := map[string]string{"ID": id, "Name": *name, "Module": *module, "Publisher": *pub, "SDK": SDKVersion}
	for path, body := range files {
		full := filepath.Join(*dir, path)
		if _, err := os.Stat(full); err == nil {
			die("%s already exists", full)
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		f, err := os.Create(full)
		if err != nil {
			die("%v", err)
		}
		if err := template.Must(template.New(path).Parse(body)).Execute(f, data); err != nil {
			die("%s: %v", path, err)
		}
		f.Close()
	}
	fmt.Printf("created %s — next: cd %s && go mod tidy && go test ./... && go run github.com/toolingdev/ptz-controller/sdk/addon/cmd/ptz-addon build\n", *dir, *dir)
}
