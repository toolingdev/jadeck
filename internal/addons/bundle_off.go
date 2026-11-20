//go:build !addonbundle

package addons

import "embed"

// Dev builds ship no add-ons: install them from Settings → Add-ons, or
// load a project folder in dev mode.
var bundled embed.FS
