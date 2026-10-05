package app

import (
	"io/fs"
	"net/http"
	"strings"
	"video-rename/web"
)

func assetHandler() http.Handler {
	assets, _ := fs.Sub(web.Assets, "dist")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
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
				http.Error(w, "资源不可用", 500)
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
