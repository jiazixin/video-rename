package app

import (
	"cmp"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

var (
	videoExts = "|mp4|m4v|mkv|mov|avi|webm|flv|wmv|mpg|mpeg|ts|m2ts|mts|3gp|ogv|rmvb|asf|vob|"
	imageExts = "|jpg|jpeg|png|gif|webp|avif|bmp|"
	audioExts = "|mp3|m4a|m4b|aac|flac|wav|wave|ogg|oga|opus|wma|aiff|aif|caf|"
	// Audio containers get inconsistent sniffed and system mime types across
	// platforms, so the served content type is fixed per extension.
	audioTypes = map[string]string{
		"mp3": "audio/mpeg", "m4a": "audio/mp4", "m4b": "audio/mp4", "aac": "audio/aac",
		"flac": "audio/flac", "wav": "audio/wav", "wave": "audio/wav", "ogg": "audio/ogg",
		"oga": "audio/ogg", "opus": "audio/ogg", "wma": "audio/x-ms-wma",
		"aiff": "audio/aiff", "aif": "audio/aiff", "caf": "audio/x-caf",
	}
)

// playback serves authenticated, seekable media without loading it into memory.
func (a *App) playback(w http.ResponseWriter, r *http.Request) { a.serveMedia(w, r, videoExts, nil) }

func (a *App) imagePreview(w http.ResponseWriter, r *http.Request) {
	a.serveMedia(w, r, imageExts, func(ext string) string { return mime.TypeByExtension("." + ext) })
}

func (a *App) audioPlayback(w http.ResponseWriter, r *http.Request) {
	a.serveMedia(w, r, audioExts, func(ext string) string { return cmp.Or(audioTypes[ext], "application/octet-stream") })
}

func (a *App) serveMedia(w http.ResponseWriter, r *http.Request, allowed string, contentType func(string) string) {
	id := number(r.PathValue("id"))
	e, err := a.Library.Get(id)
	if err != nil {
		failure(w, err)
		return
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(e.Name)), ".")
	if e.Kind != "file" || !strings.Contains(allowed, "|"+ext+"|") {
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
	if contentType != nil {
		if t := contentType(ext); t != "" {
			w.Header().Set("Content-Type", t)
		}
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
