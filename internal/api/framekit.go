package api

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// The shared frame kit (design/WIDGETS-V2.md "Shared frame kit"): theme
// tokens and the standard controls (header, buttons, hold to confirm,
// segmented, fader strip, knob, LED meter, button grid) for add-on frame
// widgets, so add-ons that use it look and behave the same.
//
// It's optional. A frame opts in with
//   <link rel="stylesheet" href="/api/kit/v1/kit.css">
//   <script src="/api/kit/v1/kit.js"></script>
// and frames that don't load it are unaffected: nothing in the frame CSP,
// the bridge or the manifest depends on it. It is served from the same
// origin as the frame's own files, so the frame CSP (script-src 'self')
// already allows it, and it holds no data, so it needs no key.
//
// Versioned by path: v1 only ever changes compatibly. A breaking change
// is a new folder (v2) served next to v1.

//go:embed kit
var kitFS embed.FS

type kitFile struct {
	data []byte
	ct   string
	etag string
}

var kitFiles = loadKit()

func loadKit() map[string]kitFile {
	m := map[string]kitFile{}
	fs.WalkDir(kitFS, "kit", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ct := uiTypes[strings.ToLower(path.Ext(p))]
		if ct == "" {
			return nil
		}
		b, err := kitFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		m[strings.TrimPrefix(p, "kit/")] = kitFile{data: b, ct: ct, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		return nil
	})
	return m
}

// GET /api/kit/{path...}, e.g. /api/kit/v1/kit.js
func (s *Server) frameKit(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", uiCSP)
	f, ok := kitFiles[r.PathValue("path")]
	if !ok {
		fail(w, 404, "not found")
		return
	}
	h.Set("Content-Type", f.ct)
	// An hour fresh, then revalidated by ETag: a jadeck update reaches
	// frames within the hour without re-downloading unchanged files.
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("ETag", f.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.data))
}
