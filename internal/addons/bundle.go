package addons

import (
	"crypto/ed25519"
	"io/fs"

	"github.com/toolingdev/jadeck/sdk/addon/pack"
)

// InstallBundled installs the add-ons this release ships with, when one
// isn't installed yet or the bundled version is newer, so a fresh
// install works offline and an app update brings add-on fixes along.
// A bundled package must be signed by pub (when pub is set).
func (h *Host) InstallBundled(pub ed25519.PublicKey) {
	ents, _ := fs.Glob(bundled, "bundle/*.ptzaddon")
	for _, name := range ents {
		data, err := fs.ReadFile(bundled, name)
		if err != nil {
			continue
		}
		p, err := pack.Read(data, pub)
		if err != nil {
			h.log.Error("bundled add-on unreadable", "file", name, "err", err)
			continue
		}
		if im, _, ok := h.installed(p.Manifest.ID); ok && !newer(p.Manifest.Version, im.Version) {
			continue
		}
		if len(pub) > 0 && !p.Verified {
			h.log.Error("bundled add-on is not signed by the release key — skipped", "addon", p.Manifest.ID)
			continue
		}
		if err := h.Install(p, true); err != nil {
			h.log.Error("bundled add-on not installed", "addon", p.Manifest.ID, "err", err)
			continue
		}
		h.log.Info("add-on installed from this release", "addon", p.Manifest.ID, "version", p.Manifest.Version)
	}
}
