// Package web embeds the browser UI into the binary, so a release is one
// file. dist/ is the React app (built from ../frontend with `npm run
// build`); legacy/ is the previous vanilla-JS UI, kept at /legacy during
// the side-by-side trial.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

//go:embed legacy
var legacyFS embed.FS

// Dist is the new app (index.html, assets/, sw.js, manifest, icon).
var Dist = sub(distFS, "dist")

// Legacy is the previous UI.
var Legacy = sub(legacyFS, "legacy")

func sub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
