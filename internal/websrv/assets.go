package websrv

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// assets fingerprints what the browser keeps between visits: the files under
// static/ and the service worker that caches them. It is computed once from
// the embedded files, so it changes exactly when a deploy changes one of them
// and stays the same across restarts of the same build.
type assets struct {
	// version names the service worker's caches. A new version makes the
	// browser install the new worker, which fetches every shell asset again
	// and drops the old caches.
	version string
	// etags holds one strong validator per static file, keyed by its path
	// below static/.
	etags map[string]string
}

// fingerprint hashes every file below static/ and the service worker template.
// Each file contributes its path and length as well as its bytes, so renaming,
// adding or removing a file changes the version too.
func fingerprint(static, templates fs.FS) (assets, error) {
	a := assets{etags: map[string]string{}}
	all := sha256.New()
	add := func(name string, body []byte) {
		all.Write([]byte(name))
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(body)))
		all.Write(n[:])
		all.Write(body)
	}
	err := fs.WalkDir(static, "static", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(static, name)
		if err != nil {
			return err
		}
		add(name, body)
		sum := sha256.Sum256(body)
		a.etags[strings.TrimPrefix(name, "static/")] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	if err != nil {
		return assets{}, err
	}
	worker, err := fs.ReadFile(templates, path.Join("templates", "sw.js.tmpl"))
	if err != nil {
		return assets{}, err
	}
	add("templates/sw.js.tmpl", worker)
	a.version = hex.EncodeToString(all.Sum(nil)[:6])
	return a, nil
}

// serveStatic serves the embedded static files. Their URLs carry no version,
// so a browser must not keep a copy without asking: no-cache makes it
// revalidate every time, and the ETag lets an unchanged file answer with an
// empty 304 instead of its bytes. Offline and instant loads come from the
// service worker's cache, which a new deploy replaces.
func (a assets) serveStatic(files http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if tag, ok := a.etags[r.URL.Path]; ok {
			// http.FileServer compares If-None-Match against this header.
			w.Header().Set("ETag", tag)
		}
		files.ServeHTTP(w, r)
	})
}
