package addons

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// UIFile reads one of an add-on's ui/ files (frame widgets), for the
// installed or dev-loaded version. name is the path inside ui/
// ("live.html", "img/logo.png"). Only plain files of the allowed types
// are served; a directory, a path leaving ui/ or a symlink pointing out
// of it is ErrNotFound. Add-ons that are turned off serve nothing.
func (h *Host) UIFile(id, name string) ([]byte, time.Time, error) {
	if !fs.ValidPath(name) || name == "." || !addon.UIFileAllowed(path.Join("ui", name)) {
		return nil, time.Time{}, ErrNotFound
	}
	if fsys, ok := h.inProcessUI[id]; ok {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, time.Time{}, ErrNotFound
		}
		return b, time.Time{}, nil
	}
	r, ok := h.resolve(id)
	if !ok || r.dir == "" || !h.Enabled(id) {
		return nil, time.Time{}, ErrNotFound
	}
	root, err := os.OpenRoot(filepath.Join(r.dir, "ui")) // no escaping ui/, symlinks included
	if err != nil {
		return nil, time.Time{}, ErrNotFound
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, time.Time{}, ErrNotFound
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, time.Time{}, ErrNotFound
	}
	b, err := io.ReadAll(io.LimitReader(f, addon.MaxUISize+1))
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(b) > addon.MaxUISize {
		return nil, time.Time{}, errors.New("ui file is too big")
	}
	return b, st.ModTime(), nil
}

// SetInProcessUI gives an in-process add-on (tests) its ui/ files.
func (h *Host) SetInProcessUI(id string, fsys fs.FS) {
	if h.inProcessUI == nil {
		h.inProcessUI = map[string]fs.FS{}
	}
	h.inProcessUI[id] = fsys
}
