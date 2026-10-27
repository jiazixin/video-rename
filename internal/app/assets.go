package app

import (
	"io/fs"
	"net/http"
	"strings"

	"video-rename/web"
)

func assetHandler() http.Handler {
	assets, err := web.Files()
	if err != nil {
		// fs.Sub fails only for an invalid literal directory name.
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(assets, name); err != nil {
			if strings.Contains(name, ".") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")

			data, err := fs.ReadFile(assets, name)
			if err != nil {
				http.Error(w, "asset unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(data)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + name
		http.FileServer(http.FS(assets)).ServeHTTP(w, r2)
	})
}
