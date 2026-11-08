// Package web embeds the built single-page app (ADR-0022) and serves it.
//
// `make web` builds the app into web/dist. Git tracks only an empty
// web/dist/.gitkeep, so `go build` works on a checkout that never ran Node;
// a binary built that way answers with a notice instead of the app.
package web

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Dist is the build output, rooted at web/dist.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" is embedded above, so it exists
	}
	return sub
}

// Built reports whether fsys holds a build of the app.
func Built(fsys fs.FS) bool {
	info, err := fs.Stat(fsys, "index.html")
	return err == nil && !info.IsDir()
}

const notBuilt = "The web app is not built into this binary.\nRun `make web`, then `make build`.\n"

// Handler serves fsys as a single-page app. A file that exists is served as
// it is; a path without a file extension that matches nothing is a client-side
// route, so it gets index.html and the router decides; a missing file such as
// an old /assets/app-1a2b3c.js is a plain 404. Only GET and HEAD are served.
func Handler(fsys fs.FS) http.Handler { return handler{fsys} }

type handler struct{ fsys fs.FS }

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if hidden(name) {
		http.NotFound(w, r)
		return
	}
	if h.serve(w, r, name) {
		return
	}
	if path.Ext(name) != "" && name != "index.html" {
		http.NotFound(w, r)
		return
	}
	if !Built(h.fsys) {
		http.Error(w, notBuilt, http.StatusServiceUnavailable)
		return
	}
	h.serve(w, r, "index.html")
}

// hidden reports whether any element of name starts with a dot (.gitkeep).
func hidden(name string) bool {
	for elem := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(elem, ".") {
			return true
		}
	}
	return false
}

// serve writes the regular file `name`, or reports false if there is none.
func (h handler) serve(w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := h.fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "cannot read "+name, http.StatusInternalServerError)
			return true
		}
		rs = bytes.NewReader(data)
	}
	hdr := w.Header()
	hdr.Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(name, "assets/") {
		// Vite names these after their content, so a changed file has a new name.
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// index.html names the current assets: always ask again.
		hdr.Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), rs)
	return true
}
