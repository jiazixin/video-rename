package prompts

import (
	"path/filepath"
	"strings"
	"testing"
	"video-rename/internal/storage"
)

func TestSaveValidationConflictAndCap(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Store{db}
	first, err := s.Save(Profile{Title: " 旅行 ", Body: " 描述风景 "})
	if err != nil || first.Title != "旅行" || first.Body != "描述风景" || first.Version != 1 {
		t.Fatal(first, err)
	}
	if _, err = s.Save(Profile{Title: " ", Body: "x"}); err == nil {
		t.Fatal("blank title accepted")
	}
	if _, err = s.Save(Profile{Title: "x", Body: " "}); err == nil {
		t.Fatal("blank body accepted")
	}
	if _, err = s.Save(Profile{Title: "x", Body: strings.Repeat("字", 16001)}); err == nil {
		t.Fatal("oversized body accepted")
	}
	if _, err = s.Save(Profile{Title: strings.Repeat("字", 81), Body: "x"}); err == nil {
		t.Fatal("oversized title accepted")
	}
	stale := first
	stale.Title = "改名"
	updated, err := s.Save(stale)
	if err != nil || updated.Title != "改名" || updated.Version != 2 {
		t.Fatal(updated, err)
	}
	if _, err = s.Save(first); err == nil {
		t.Fatal("stale edit accepted")
	}
	if _, err = s.Save(Profile{ID: 99999, Title: "失踪", Body: "x", Version: 1}); err == nil {
		t.Fatal("missing row accepted")
	}
	if _, err = s.Get(99999); err == nil {
		t.Fatal("missing get accepted")
	}
}

func TestDeleteGuardedByModelConfigs(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Store{db}
	p, err := s.Save(Profile{Title: "被引用", Body: "正文"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(p.ID, p.Version+1); err == nil {
		t.Fatal("stale delete accepted")
	}
	if err = s.Delete(99999, 1); err == nil {
		t.Fatal("missing delete accepted")
	}
	if _, err = db.Exec("INSERT INTO model_configs(title,prompt_id,created,updated,is_default) VALUES('引用配置',?, 'now','now',1)", p.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(p.ID, p.Version); err == nil || !strings.Contains(err.Error(), "1 个模型配置") {
		t.Fatal("referenced prompt deleted", err)
	}
	if _, err = db.Exec("UPDATE model_configs SET prompt_id=0"); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(p.ID, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(p.ID); err == nil {
		t.Fatal("still exists")
	}
}
