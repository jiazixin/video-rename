package systemsettings

import (
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"video-rename/internal/storage"
)

func TestFailureKeywordsDefaultsEmptyPersistenceAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keywords.sqlite")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	store := Store{DB: db}
	got, err := store.FailureKeywords()
	if err != nil || !slices.Equal(got, []string{"命名失败", "拒绝", "无法完成", "sorry"}) {
		t.Fatal(got, err)
	}
	got[0] = "changed"
	if !slices.Equal(DefaultFailureKeywords(), []string{"命名失败", "拒绝", "无法完成", "sorry"}) {
		t.Fatal("default list shared mutable memory")
	}
	if err := store.Save(Default()); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"failure_keywords":null}`} {
		if _, err := db.Exec("UPDATE system_settings SET policy=?", raw); err != nil {
			t.Fatal(err)
		}
		got, err := store.FailureKeywords()
		if err != nil || !slices.Equal(got, DefaultFailureKeywords()) {
			t.Fatalf("missing/null did not use defaults: %s %v %v", raw, got, err)
		}
	}
	if saved, err := store.SaveFailureKeywords([]string{}); err != nil || saved == nil || len(saved) != 0 {
		t.Fatalf("empty save: %v %v", saved, err)
	}
	if err := store.Save(Default()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScan(DefaultScanSettings()); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow("SELECT policy FROM system_settings WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	if value, ok := fields["failure_keywords"].([]any); !ok || len(value) != 0 {
		t.Fatalf("empty rules not stored explicitly: %s", raw)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store = Store{DB: db}
	got, err = store.FailureKeywords()
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty rules lost after restart: %v %v", got, err)
	}
	if _, err = store.SaveFailureKeywords(DefaultFailureKeywords()); err != nil {
		t.Fatal(err)
	}
	got, err = store.FailureKeywords()
	if err != nil || !slices.Equal(got, DefaultFailureKeywords()) {
		t.Fatalf("restore defaults failed: %v %v", got, err)
	}
}

func TestFailureKeywordsNormalizationAndValidation(t *testing.T) {
	got, err := NormalizeFailureKeywords([]string{"  sorry ", "SORRY", " 无法完成 ", "无法完成", "I cannot help with that."})
	if err != nil || !slices.Equal(got, []string{"sorry", "无法完成", "I cannot help with that."}) {
		t.Fatal(got, err)
	}
	for _, invalid := range [][]string{nil, {""}, {" \n\t　 "}, {strings.Repeat("字", 201)}, {string([]byte{0xff})}, make([]string, 101)} {
		if _, err := NormalizeFailureKeywords(invalid); err == nil {
			t.Fatalf("invalid rules accepted: %q", invalid)
		}
	}
	valid := make([]string, 100)
	for i := range valid {
		valid[i] = strings.Repeat("字", 200)
	}
	if got, err := NormalizeFailureKeywords(valid); err != nil || len(got) != 1 {
		t.Fatal("100 rules or 200 Unicode characters rejected", got, err)
	}
}

func TestConcurrentFailureKeywordScanAndProcessingSaves(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := Store{DB: db}
	keywords := []string{"自定义失败句子", "I cannot help"}
	scan := ScanSettings{Enabled: false, IntervalMinutes: 90, Targets: []int64{1, 2}, ExcludedTargets: []int64{}}
	processing := Default()
	processing.ExtractWorkers, processing.RetryCount = 4, 7
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := store.SaveFailureKeywords(keywords); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := store.SaveScan(scan); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := store.Save(processing); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	gotKeywords, err := store.FailureKeywords()
	if err != nil || !slices.Equal(gotKeywords, keywords) {
		t.Fatalf("lost keyword save: %v %v", gotKeywords, err)
	}
	gotScan, err := store.Scan()
	if err != nil || gotScan.Enabled != scan.Enabled || gotScan.IntervalMinutes != scan.IntervalMinutes || !slices.Equal(gotScan.Targets, scan.Targets) {
		t.Fatalf("lost scan save: %+v %v", gotScan, err)
	}
	gotProcessing, err := store.Get()
	if err != nil || gotProcessing != processing {
		t.Fatalf("lost processing save: %+v %v", gotProcessing, err)
	}
	if _, err := db.Exec(`UPDATE system_settings SET policy='{"failure_keywords":[" "]}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailureKeywords(); err == nil {
		t.Fatal("invalid stored keywords silently accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailureKeywords(); err == nil {
		t.Fatal("closed database returned defaults")
	}
}
