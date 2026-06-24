package api

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var uiFS embed.FS

// errUIMissing is the sentinel returned by CheckUIBundled when the embed
// doesn't contain a dist/ directory with an index.html — the symptom of
// building the Go binary without `npm run build` first. Callers should
// treat this as fatal in release builds and a warning during local dev.
var errUIMissing = errors.New("ui bundle is empty: build the UI first (cd ui && npm run build)")

// CheckUIBundled returns nil when the embedded UI contains a usable
// dist/index.html, or errUIMissing otherwise. go:embed of a missing
// directory compiles cleanly, so a binary built against an empty dist
// would otherwise boot, log nothing, and serve 404s to every route.
func CheckUIBundled() error {
	sub, err := fs.Sub(uiFS, "dist")
	if err != nil {
		return errUIMissing
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return errUIMissing
	}
	return nil
}

func UIHandler() http.Handler {
	sub, err := fs.Sub(uiFS, "dist")
	if err != nil {
		sub = uiFS
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		if _, err := fs.Stat(sub, reqPath); err != nil {
			reqPath = "index.html"
		}
		f, err := sub.Open(reqPath)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer f.Close()
		stat, _ := f.Stat()
		if stat != nil && stat.IsDir() {
			reqPath = path.Join(reqPath, "index.html")
			f.Close()
			f, err = sub.Open(reqPath)
			if err != nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			defer f.Close()
		}
		ext := path.Ext(reqPath)
		if ct := mime.TypeByExtension(ext); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.Copy(w, f)
	})
}