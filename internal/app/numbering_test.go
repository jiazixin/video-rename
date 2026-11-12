package app

import (
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/library"
	"video-rename/internal/systemsetting"
)

func TestImageNumberingAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "photo.PNG"), []byte("picture"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(t.Context(), &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()
	var cookies []*http.Cookie
	request := func(method, path, body string, sameSite bool) *httptest.ResponseRecorder {
		r := jsonRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if !sameSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/directories/1/numberings/preview", `{}`, true); w.Code != 401 && w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`, true)
	cookies = w.Result().Cookies()
	var session map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if w = request("POST", "/api/directories/1/numberings/preview", `{}`, false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	w = request("POST", "/api/directories/1/numberings/preview", `{"media":"image","query":"","sort":"name_desc"}`, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var b library.Numbering
	if err = json.Unmarshal(w.Body.Bytes(), &b); err != nil || len(b.Items) != 1 || b.Items[0].New != "1.png" {
		t.Fatal(b, err)
	}
	if strings.Contains(w.Body.String(), "identity") || strings.Contains(w.Body.String(), "temp_name") {
		t.Fatal("internal journal exposed")
	}
	for _, action := range []string{"restore-preview", "recover"} {
		if w = request("POST", "/api/numberings/"+b.ID+"/"+action, `{}`, false); w.Code != 403 {
			t.Fatal("cross-site write accepted", action, w.Code)
		}
	}
	if w = request("POST", "/api/numbering/jobs", `{"action":"apply","parent":1,"batches":["`+b.ID+`"]}`, false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	if w = request("POST", "/api/numberings/"+b.ID+"/apply", `{}`, true); w.Code != 404 {
		t.Fatalf("got %d for the retired synchronous apply, want 404", w.Code)
	}
	// Applying a committed batch again reports the same result.
	for _, key := range []string{"image-apply-1", "image-apply-2"} {
		if s := applyNumberingTest(t, a, key, b.ID); s.Status != "completed" || s.Done != 1 || s.Files != 1 {
			t.Fatalf("got %+v, want one completed directory with one renamed file", s)
		}
	}
	w = request("GET", "/api/directories/1/numberings?media=image", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "committed") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/api/numberings/"+b.ID+"/restore-preview", `{}`, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if s := applyNumberingTest(t, a, "image-restore", b.ID); s.Status != "completed" {
		t.Fatalf("got %+v, want completed restore", s)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "photo.PNG")); err != nil || string(raw) != "picture" {
		t.Fatal(string(raw), err)
	}
}

func TestNumberingSettingsAPI(t *testing.T) {
	root := t.TempDir()
	a, err := New(t.Context(), &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	handler := a.Handler()
	loginReq := jsonRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, loginReq)
	var session map[string]string
	json.Unmarshal(login.Body.Bytes(), &session)
	request := func(method, path, body string, sameSite bool) *httptest.ResponseRecorder {
		r := jsonRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range login.Result().Cookies() {
			r.AddCookie(c)
		}
		if !sameSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/numbering/settings", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"padding":1`) || !strings.Contains(w.Body.String(), `"lowercase":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("PUT", "/api/numbering/settings", `{"image":{"padding":3,"lowercase":true,"prefix":"IMG_","compact":false},"audio":{"padding":1,"lowercase":false},"shared":true}`, false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	if w = request("PUT", "/api/numbering/settings", `{"image":{"padding":3,"lowercase":true,"prefix":"IMG_","compact":false},"audio":{"padding":1,"lowercase":false},"shared":true}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/api/numbering/settings", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"prefix":"IMG_"`) || !strings.Contains(w.Body.String(), `"shared":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("PUT", "/api/numbering/settings", `{"image":{"padding":0,"lowercase":true},"audio":{"padding":1,"lowercase":true},"shared":false}`, true); w.Code == 200 {
		t.Fatal("invalid padding accepted")
	}
}

func TestAudioNumberingAPI(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "track.MP3"), []byte("audio"), 0o600)
	os.WriteFile(filepath.Join(root, "cover.png"), []byte("cover"), 0o600)
	a, err := New(t.Context(), &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/directories/1/numberings?media=video", nil))
	if unauth.Code != 401 {
		t.Fatal("unprotected", unauth.Code)
	}
	loginReq := jsonRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, loginReq)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := jsonRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range login.Result().Cookies() {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/directories/1/numberings/preview", `{"media":"video","query":"","sort":"name_asc"}`); w.Code != 400 {
		t.Fatal("invalid media accepted", w.Code)
	}
	w := request("POST", "/api/directories/1/numberings/preview", `{"media":"audio","query":"","sort":"name_asc"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var b library.Numbering
	if err = json.Unmarshal(w.Body.Bytes(), &b); err != nil || len(b.Items) != 1 || b.Items[0].New != "1.mp3" {
		t.Fatal(b, err)
	}
	if s := applyNumberingTest(t, a, "audio-apply", b.ID); s.Status != "completed" {
		t.Fatalf("got %+v, want completed numbering", s)
	}
	if _, err = os.Stat(filepath.Join(root, "cover.png")); err != nil {
		t.Fatal("image touched by audio numbering", err)
	}
	if w = request("GET", "/api/directories/1/numberings?media=audio", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "committed") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/api/directories/1/numberings?media=image", ""); w.Code != 200 || strings.Contains(w.Body.String(), "committed") {
		t.Fatal("audio batch leaked into image history", w.Code)
	}
}

func TestNumberingFailurePreservesWrappedDiagnostics(t *testing.T) {
	err := fmt.Errorf("parent/child: %w", &domain.JobError{Code: domain.CodeRenameConflict, Message: "target is occupied", ProcessingLog: "original name: photo.jpg\ntarget type: directory"})
	w := httptest.NewRecorder()
	numberingFailure(w, httptest.NewRequest("POST", "/api/numberings/x/recover", nil), err)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				ProcessingLog string `json:"processing_log"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusConflict || body.Error.Code != "rename_conflict" || !strings.Contains(body.Error.Message, "parent/child") || !strings.Contains(body.Error.Details.ProcessingLog, "target type: directory") {
		t.Fatal(w.Code, body)
	}
}

// applyNumberingTest applies previewed batches through a numbering job and
// returns the finished job status.
func applyNumberingTest(t *testing.T, a *App, key string, batches ...string) numberingStatus {
	t.Helper()
	numberingRequestTest(t, numberingRequestTestOptions{a: a, req: numberingRequest{Key: key, Action: "apply", Parent: 1, Batches: batches}, want: http.StatusAccepted})
	return waitNumberingTest(t, a, key)
}

// A directory of this size used to exceed the retained response limit, which
// turned a successful keyed preview into 500 and left its key pending.
func TestLargeDirectoryNumberingPreviewIsPaged(t *testing.T) {
	const numbered, unnumbered = 8000, 3
	c := newContractClient(t)
	root := c.app.Library.Root
	for i := range numbered {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("IMG_20240101_%06d.JPG", i+1)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range unnumbered {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("zz-holiday-%d.JPG", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// With this rule the numbered files keep their names; only the three files
	// sorted after them move.
	settings := domain.DefaultNumberingSettings()
	settings.Image = domain.MediaNumberingRules{Prefix: "IMG_20240101_", Padding: 6, Compact: true}
	if err := systemsetting.New(c.app.db).SaveNumbering(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if err := c.app.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	preview := contractRequest{method: http.MethodPost, path: "/api/directories/1/numberings/preview", body: `{"media":"image","query":"","status":"","sort":"name_asc"}`, key: "large-preview"}
	first := c.call(preview)
	requireContractStatus(t, first, http.StatusCreated)
	var b library.Numbering
	if err := json.Unmarshal(first.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if b.Total != numbered+unnumbered || b.Changed != unnumbered || len(b.Items) != library.NumberingPageSize {
		t.Fatalf("got total=%d changed=%d items=%d, want %d, %d and the first %d items", b.Total, b.Changed, len(b.Items), numbered+unnumbered, unnumbered, library.NumberingPageSize)
	}
	if replay := c.call(preview); replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("got replay %d, want the retained 201 response", replay.Code)
	}
	seen := map[int64]bool{}
	renamed := map[string]string{}
	for offset := 0; offset < b.Total; offset += library.NumberingPageSize {
		response := c.call(contractRequest{method: http.MethodGet, path: fmt.Sprintf("/api/numberings/%s/items?offset=%d", b.ID, offset)})
		requireContractStatus(t, response, http.StatusOK)
		var page library.NumberingPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if page.Total != b.Total || page.Offset != offset || page.Limit != library.NumberingPageSize {
			t.Fatalf("got page total=%d offset=%d limit=%d at offset %d", page.Total, page.Offset, page.Limit, offset)
		}
		for _, item := range page.Items {
			seen[item.Entry] = true
			if item.Old != item.New {
				renamed[item.Old] = item.New
			}
		}
	}
	if len(seen) != b.Total || len(renamed) != unnumbered {
		t.Fatalf("got %d distinct items and %d renames over all pages, want %d and %d", len(seen), len(renamed), b.Total, unnumbered)
	}
	last := c.call(contractRequest{method: http.MethodGet, path: "/api/numberings/" + b.ID + "/items?offset=999999"})
	requireContractStatus(t, last, http.StatusOK)
	var page library.NumberingPage
	if err := json.Unmarshal(last.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Offset != 8000 || len(page.Items) != unnumbered {
		t.Fatalf("got offset=%d with %d items past the end, want the last page", page.Offset, len(page.Items))
	}
	requireContractStatus(t, c.call(contractRequest{method: http.MethodGet, path: "/api/numberings/missing/items"}), http.StatusNotFound)
	if s := applyNumberingTest(t, c.app, "large-apply", b.ID); s.Status != "completed" || s.Files != unnumbered {
		t.Fatalf("got %+v, want %d renamed files", s, unnumbered)
	}
	for old, name := range renamed {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s was not renamed to %s: %v", old, name, err)
		}
	}
	if got := pendingReservations(t, c.app); got != 0 {
		t.Fatalf("got %d pending reservations, want 0", got)
	}
}
