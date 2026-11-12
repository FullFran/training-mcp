package websrv

import (
	"regexp"
	"testing"
	"testing/fstest"
)

func assetTree() fstest.MapFS {
	return fstest.MapFS{
		"static/app.js":        {Data: []byte("console.log('app')")},
		"static/app.css":       {Data: []byte("body{}")},
		"static/icon-192.png":  {Data: []byte{0x89, 'P', 'N', 'G'}},
		"templates/sw.js.tmpl": {Data: []byte("const VERSION = '{{.AssetVersion}}';")},
		"templates/index.html": {Data: []byte("<html>")},
	}
}

func versionOf(t *testing.T, tree fstest.MapFS) string {
	t.Helper()
	a, err := fingerprint(tree, tree)
	if err != nil {
		t.Fatal(err)
	}
	return a.version
}

// The service worker's version follows the files it caches: the same build
// keeps it, and any change to a static file or to the worker makes a new one,
// so the browser installs the new worker and refreshes its offline copies.
func TestAssetVersionChangesWithEveryStaticFileAndOnlyThen(t *testing.T) {
	before := versionOf(t, assetTree())
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(before) {
		t.Fatalf("version = %q, want 12 hex digits", before)
	}
	if again := versionOf(t, assetTree()); again != before {
		t.Fatalf("an unchanged build changed the version: %s then %s", before, again)
	}

	changes := map[string]func(fstest.MapFS){
		"edit a script":     func(m fstest.MapFS) { m["static/app.js"] = &fstest.MapFile{Data: []byte("console.log('app 2')")} },
		"edit a stylesheet": func(m fstest.MapFS) { m["static/app.css"] = &fstest.MapFile{Data: []byte("body{color:red}")} },
		"edit an image":     func(m fstest.MapFS) { m["static/icon-192.png"] = &fstest.MapFile{Data: []byte{0x89, 'P', 'N', 'G', 0}} },
		"edit the worker": func(m fstest.MapFS) {
			m["templates/sw.js.tmpl"] = &fstest.MapFile{Data: []byte("// new\nconst VERSION = '{{.AssetVersion}}';")}
		},
		"add a static file":    func(m fstest.MapFS) { m["static/new.js"] = &fstest.MapFile{Data: []byte("")} },
		"remove a static file": func(m fstest.MapFS) { delete(m, "static/app.css") },
		"rename a static file": func(m fstest.MapFS) { m["static/main.js"] = m["static/app.js"]; delete(m, "static/app.js") },
		"move bytes across two": func(m fstest.MapFS) {
			m["static/app.css"] = &fstest.MapFile{Data: []byte("body{}x")}
			m["static/app.js"] = &fstest.MapFile{Data: []byte("onsole.log('app')")}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			tree := assetTree()
			change(tree)
			if after := versionOf(t, tree); after == before {
				t.Fatalf("version stayed %s", before)
			}
		})
	}

	t.Run("a page template is not cached by the worker", func(t *testing.T) {
		tree := assetTree()
		tree["templates/index.html"] = &fstest.MapFile{Data: []byte("<html lang=es>")}
		if after := versionOf(t, tree); after != before {
			t.Fatalf("version changed from %s to %s", before, after)
		}
	})
}

// Each static file gets its own validator, which changes with its bytes only.
func TestStaticETagsFollowEachFile(t *testing.T) {
	a, err := fingerprint(assetTree(), assetTree())
	if err != nil {
		t.Fatal(err)
	}
	tree := assetTree()
	tree["static/app.js"] = &fstest.MapFile{Data: []byte("console.log('app 2')")}
	b, err := fingerprint(tree, tree)
	if err != nil {
		t.Fatal(err)
	}
	if a.etags["app.js"] == "" || a.etags["app.js"] == b.etags["app.js"] {
		t.Fatalf("app.js etag %q then %q", a.etags["app.js"], b.etags["app.js"])
	}
	if a.etags["app.css"] != b.etags["app.css"] {
		t.Fatalf("app.css etag changed though the file did not: %q then %q", a.etags["app.css"], b.etags["app.css"])
	}
}
