package app

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/models"
	"video-rename/internal/systemsettings"
)

func TestSettingsReloadReadsWhileWriterBusy(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Workers: 1, Auth: config.AuthConfig{Username: "admin", Password: "test-password"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	settings := systemsettings.Default()
	settings.ExtractWorkers, settings.CacheGB, settings.StabilitySeconds = 3, 2, 17
	store := systemsettings.Store{DB: a.DB}
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	numbering := domain.DefaultNumberingSettings()
	numbering.Image.Prefix, numbering.Audio.Prefix, numbering.Shared = "IMAGE_", "AUDIO_", true
	if err := store.SaveNumbering(numbering); err != nil {
		t.Fatal(err)
	}
	modelStore := models.Store{DB: a.DB}
	profile, err := modelStore.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	profile.BaseURL, profile.Model, profile.Title = "http://localhost/v1", "committed-model", "committed profile"
	profile, err = modelStore.Save(profile)
	if err != nil {
		t.Fatal(err)
	}

	handler := a.Handler()
	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(login, loginRequest)
	if login.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", login.Code, login.Body.String())
	}
	var session struct {
		Username string `json:"username"`
		CSRF     string `json:"csrf"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.Username != "admin" || session.CSRF == "" {
		t.Fatalf("login session: %+v err=%v", session, err)
	}
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not issue a session cookie")
	}

	tx, err := a.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), "UPDATE system_settings SET extract_workers=21,cache_gb=99,numbering=? WHERE id=1", `{"image":{"padding":1,"prefix":"UNCOMMITTED_","lowercase":true,"compact":true},"audio":{"padding":1,"prefix":"UNCOMMITTED_","lowercase":true,"compact":true},"shared":false}`); err != nil {
		t.Fatal(err)
	}
	uncommittedModel := profile.ServerConfig
	uncommittedModel.Model = "uncommitted-model"
	raw, err := json.Marshal(uncommittedModel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), "UPDATE model_configs SET title='uncommitted profile',settings=? WHERE id=?", string(raw), profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), "UPDATE sessions SET csrf='uncommitted-csrf',expires=0"); err != nil {
		t.Fatal(err)
	}

	// A fresh login avoids session renewal writes. Every request must use its
	// own committed WAL view while the sole writer still holds these updates.
	paths := []string{"/api/auth/session", "/api/system", "/api/system/settings", "/api/model-configs", "/api/numbering/settings"}
	type response struct {
		path string
		w    *httptest.ResponseRecorder
	}
	responses := make(chan response, len(paths))
	ctx, cancel := context.WithCancel(t.Context())
	remaining := len(paths)
	defer func() {
		// Release the writer before draining failed requests. Otherwise an
		// accidental writer-pool read can also prevent App.Close from returning.
		cancel()
		_ = tx.Rollback()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		for remaining > 0 {
			select {
			case <-responses:
				remaining--
			case <-deadline.C:
				t.Errorf("%d settings requests did not exit after cancellation and writer rollback", remaining)
				return
			}
		}
	}()
	for _, path := range paths {
		go func() {
			r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
			for _, cookie := range cookies {
				r.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			responses <- response{path: path, w: w}
		}()
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for remaining > 0 {
		var result response
		select {
		case result = <-responses:
			remaining--
		case <-deadline.C:
			t.Errorf("%d settings reload requests waited for the writer", remaining)
			return
		}
		w := result.w
		if w.Code != http.StatusOK {
			t.Errorf("%s: status=%d body=%s", result.path, w.Code, w.Body.String())
			continue
		}
		switch result.path {
		case "/api/auth/session":
			var got struct {
				Username string `json:"username"`
				CSRF     string `json:"csrf"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got != session {
				t.Errorf("session did not read committed values: got=%+v err=%v", got, err)
			}
		case "/api/system":
			var got struct {
				RootID int64  `json:"root_id"`
				Model  string `json:"model"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.RootID != 1 || got.Model != profile.Model {
				t.Errorf("system did not read committed model: got=%+v err=%v", got, err)
			}
		case "/api/system/settings":
			var got struct {
				Settings systemsettings.Settings `json:"settings"`
				Cache    struct {
					Path string `json:"path"`
				} `json:"cache"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Settings != settings || got.Cache.Path == "" {
				t.Errorf("settings did not read committed values and cache state: got=%+v err=%v", got, err)
			}
		case "/api/model-configs":
			var got []models.Profile
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].ID != profile.ID || got[0].Title != profile.Title || got[0].Model != profile.Model || !got[0].Default {
				t.Errorf("models did not read committed profile: got=%+v err=%v", got, err)
			}
		case "/api/numbering/settings":
			var got struct {
				Settings domain.NumberingSettings `json:"settings"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Settings != numbering {
				t.Errorf("numbering did not read committed values: got=%+v err=%v", got, err)
			}
		}
	}
	var heldWorkers int
	if err := tx.QueryRowContext(t.Context(), "SELECT extract_workers FROM system_settings WHERE id=1").Scan(&heldWorkers); err != nil || heldWorkers != 21 {
		t.Errorf("requests did not leave the writer transaction open: workers=%d err=%v", heldWorkers, err)
	}
}
