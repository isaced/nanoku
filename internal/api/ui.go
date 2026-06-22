package api

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var uiFS embed.FS

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