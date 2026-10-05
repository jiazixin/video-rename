package app

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"video-rename/internal/config"
)

func TestPlaybackRangeAndRestrictions(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("0123456789"), 0600)
	os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0600)
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var id int64
	a.DB.QueryRow("SELECT id FROM entries WHERE name='clip.mp4'").Scan(&id)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", fmt.Sprintf("/api/entries/%d/video", id), nil)
		r.SetPathValue("id", fmt.Sprint(id))
		r.Header.Set("Range", "bytes=2-5")
		w := httptest.NewRecorder()
		a.playback(w, r)
		return w
	}
	w := request()
	if w.Code != 206 || w.Body.String() != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("range: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/entries/%d/video", id), nil))
	if w.Code != 401 {
		t.Fatal("unprotected media", w.Code)
	}
	os.Remove(filepath.Join(root, "clip.mp4"))
	os.Symlink(filepath.Join(root, "secret.txt"), filepath.Join(root, "clip.mp4"))
	if w = request(); w.Code == 200 || w.Code == 206 {
		t.Fatal("served symlink")
	}
	a.DB.QueryRow("SELECT id FROM entries WHERE name='secret.txt'").Scan(&id)
	if w = request(); w.Code != 400 {
		t.Fatal("served non-video", w.Code)
	}
}

func TestAudioPlaybackRestrictions(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "track.mp3"), []byte("audio fixture"), 0600)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("notes"), 0600)
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var id int64
	a.DB.QueryRow("SELECT id FROM entries WHERE name='track.mp3'").Scan(&id)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", fmt.Sprintf("/api/entries/%d/audio", id), nil)
		r.SetPathValue("id", fmt.Sprint(id))
		w := httptest.NewRecorder()
		a.audioPlayback(w, r)
		return w
	}
	w := request()
	if w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("content type: %v", w.Header())
	}
	r := httptest.NewRequest("GET", fmt.Sprintf("/api/entries/%d/audio", id), nil)
	r.SetPathValue("id", fmt.Sprint(id))
	r.Header.Set("Range", "bytes=2-5")
	w = httptest.NewRecorder()
	a.audioPlayback(w, r)
	if w.Code != 206 || w.Body.String() != "dio " {
		t.Fatalf("range: %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/entries/%d/audio", id), nil))
	if w.Code != 401 {
		t.Fatal("unprotected audio", w.Code)
	}
	a.DB.QueryRow("SELECT id FROM entries WHERE name='notes.txt'").Scan(&id)
	if w = request(); w.Code != 400 {
		t.Fatal("served non-audio", w.Code)
	}
}

func TestImagePreviewRestrictions(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "photo.png"), []byte("image fixture"), 0600)
	os.WriteFile(filepath.Join(root, "active.svg"), []byte("<svg/>"), 0600)
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"photo.png", "active.svg"} {
		var id int64
		a.DB.QueryRow("SELECT id FROM entries WHERE name=?", name).Scan(&id)
		r := httptest.NewRequest("GET", fmt.Sprintf("/api/entries/%d/image", id), nil)
		r.SetPathValue("id", fmt.Sprint(id))
		w := httptest.NewRecorder()
		a.imagePreview(w, r)
		if name == "photo.png" {
			if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("image: %d %v", w.Code, w.Header())
			}
		} else if w.Code != 400 {
			t.Fatal("served SVG", w.Code)
		}
		w = httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unprotected image", w.Code)
		}
	}
}
