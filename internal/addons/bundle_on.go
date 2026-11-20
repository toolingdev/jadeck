//go:build addonbundle

package addons

import "embed"

// Release builds carry the standard add-ons (cmd/addons-bundle puts the
// signed .ptzaddon files here before go build -tags addonbundle).
//
//go:embed bundle/*.ptzaddon
var bundled embed.FS
