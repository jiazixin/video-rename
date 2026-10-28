package prompt

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func TestSaveValidationConflictAndCap(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	s := Store{db}
	first, err := s.Save(t.Context(), Profile{Title: " 旅行 ", Body: " 描述风景 "})
	if err != nil || first.Title != "旅行" || first.Body != "描述风景" || first.Version != 1 {
		t.Fatal(first, err)
	}
	if _, err = s.Save(t.Context(), Profile{Title: " ", Body: "x"}); err == nil {
		t.Fatal("blank title accepted")
	}
	if _, err = s.Save(t.Context(), Profile{Title: "x", Body: " "}); err == nil {
		t.Fatal("blank body accepted")
	}
	if _, err = s.Save(t.Context(), Profile{Title: "x", Body: strings.Repeat("字", 16001)}); err == nil {
		t.Fatal("oversized body accepted")
	}
	if _, err = s.Save(t.Context(), Profile{Title: strings.Repeat("字", 81), Body: "x"}); err == nil {
		t.Fatal("oversized title accepted")
	}
	stale := first
	stale.Title = "改名"
	updated, err := s.Save(t.Context(), stale)
	if err != nil || updated.Title != "改名" || updated.Version != 2 {
		t.Fatal(updated, err)
	}
	if _, err = s.Save(t.Context(), first); err == nil {
		t.Fatal("stale edit accepted")
	}
	if _, err = s.Save(t.Context(), Profile{ID: 99999, Title: "失踪", Body: "x", Version: 1}); err == nil {
		t.Fatal("missing row accepted")
	}
	if _, err = s.Get(t.Context(), 99999); err == nil {
		t.Fatal("missing get accepted")
	}
}

func TestDeleteGuardedByModelConfigs(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	s := Store{db}
	p, err := s.Save(t.Context(), Profile{Title: "被引用", Body: "正文"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), p.ID, p.Version+1); err == nil {
		t.Fatal("stale delete accepted")
	}
	if err = s.Delete(t.Context(), 99999, 1); err == nil {
		t.Fatal("missing delete accepted")
	}
	if _, err = storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO model_configs(title,prompt_id,created,updated,is_default) VALUES('引用配置',?, '2026-10-28T00:00:00.000Z','2026-10-28T00:00:00.000Z',1)", p.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), p.ID, p.Version); err == nil || !errors.Is(err, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonPromptInUse, Message: ""})) {
		t.Fatal("referenced prompt deleted", err)
	}
	if _, err = storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "DELETE FROM model_configs") }); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), p.ID, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(t.Context(), p.ID); err == nil {
		t.Fatal("still exists")
	}
}
