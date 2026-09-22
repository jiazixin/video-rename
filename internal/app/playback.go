package app

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// playback serves authenticated, seekable media without loading it into memory.
func (a *App) playback(w http.ResponseWriter, r *http.Request) {
	a.serveMedia(w, r, false)
}

func (a *App) imagePreview(w http.ResponseWriter, r *http.Request) { a.serveMedia(w, r, true) }

func (a *App) serveMedia(w http.ResponseWriter, r *http.Request, image bool) {
	id := number(r.PathValue("id"))
	e, err := a.Library.Get(id)
	if err != nil {
		failure(w, err)
		return
	}
	allowed := "|mp4|m4v|mkv|mov|avi|webm|flv|wmv|mpg|mpeg|ts|m2ts|mts|3gp|ogv|rmvb|asf|vob|"
	if image {
		allowed = "|jpg|jpeg|png|gif|webp|avif|bmp|"
	}
	if e.Kind != "file" || !strings.Contains(allowed, "|"+strings.TrimPrefix(strings.ToLower(filepath.Ext(e.Name)), ".")+"|") {
		http.Error(w, "Unsupported media type", 400)
		return
	}
	f, err := a.Library.OpenPlayback(id)
	if err != nil {
		failure(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if image {
		w.Header().Set("Content-Type", mime.TypeByExtension(strings.ToLower(filepath.Ext(e.Name))))
	}
	http.ServeContent(w, r, e.Name, st.ModTime(), f)
}

func (a *App) adjacentImage(w http.ResponseWriter, r *http.Request) {
	direction := r.URL.Query().Get("direction")
	if direction != "previous" && direction != "next" {
		send(w, 400, map[string]string{"message": "无效切换方向"})
		return
	}
	entry, err := a.Library.AdjacentImage(number(r.PathValue("id")), direction == "previous")
	if err != nil {
		failure(w, err)
		return
	}
	send(w, 200, entry)
}
