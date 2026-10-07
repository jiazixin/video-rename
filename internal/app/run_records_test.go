package app

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/jobs"
	"video-rename/internal/models"
	"video-rename/internal/systemsettings"
)

func TestRunDirectoryRecords(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	for id := range 2 {
		if _, err := a.DB.Exec(`INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(?,'manual','[]',0,'running','now',?)`, id+1, fmt.Sprint(id)); err != nil {
			t.Fatal(err)
		}
	}
	// Interleave directories at insertion so grouping must happen before pagination.
	for id := 1; id <= 205; id++ {
		directory, status := "上海 / ", "done"
		if id%2 == 0 {
			directory = "杭州 / "
		}
		if id%3 == 0 {
			status = "failed"
		}
		if id == 205 {
			directory, status = "", "queued"
		}
		if _, err := a.DB.Exec(`INSERT INTO run_items(id,run,entry,identity,size,mtime,status,original_path) VALUES(?,1,1,?,0,'now',?,?)`, id, fmt.Sprint(id), status, directory+fmt.Sprintf("clip-%03d.mp4", id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.DB.Exec(`INSERT INTO run_items(id,run,entry,identity,size,mtime,status,original_path) VALUES(206,2,1,'other',0,'now','failed','其他 / clip.mp4')`); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 4; n++ {
		if _, err := a.DB.Exec(`INSERT INTO model_attempts VALUES(3,?,1,'主模型','vision',?,'2026-10-07T00:00:00Z',100,'failed',?)`, n, n == 4, fmt.Sprintf("失败 %d", n)); err != nil {
			t.Fatal(err)
		}
	}
	// Exact item request keys distinguish this run's name from later rename/restore state.
	for _, op := range []struct {
		key, name, status string
		run               int
	}{
		{"run:1:item:1:0", "首次冲突.mp4", "failed", 1},
		{"run:1:item:1:9", "再次冲突.mp4", "failed", 1},
		{"run:1:item:1:10", "本批次名称.mp4", "committed", 1},
		{"run:2:item:206:0", "后续名称.mp4", "committed", 2},
		{"run:1:item:3:0", "未提交名称.mp4", "failed", 1},
		{"run:1:item:10:0", "其他条目.mp4", "committed", 1},
	} {
		if _, err := a.DB.Exec(`INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime,kind,source,status,request_key,created,run,undone) VALUES(1,1,'old.mp4',?,'x',0,'now','file','automatic',?,?,'now',?,1)`, op.name, op.status, op.key, op.run); err != nil {
			t.Fatal(err)
		}
	}
	type record struct {
		ID              int64  `json:"id"`
		Directory       string `json:"directory"`
		OriginalName    string `json:"original_name"`
		NewName         string `json:"new_name"`
		OperationStatus string `json:"operation_status"`
		Status          string `json:"status"`
		Attempts        []struct {
			Number int `json:"number"`
		} `json:"attempts"`
	}
	type dir struct {
		Path   string `json:"path"`
		Total  int    `json:"total"`
		Failed int    `json:"failed"`
	}
	type result struct {
		Items       []record `json:"items"`
		Directories []dir    `json:"directories"`
		Total       int      `json:"total"`
		Page        int      `json:"page"`
		AllTotal    int      `json:"all_total"`
		FailedTotal int      `json:"failed_total"`
	}
	read := func(query string) result {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/runs/1/items?"+query, nil)
		r.SetPathValue("id", "1")
		a.items(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
		var p result
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := read("page=1")
	if first.Total != 205 || first.AllTotal != 205 || first.FailedTotal != 68 || len(first.Directories) != 3 || len(first.Items) != 100 || first.Items[0].Status != "queued" {
		t.Fatalf("whole batch: %+v", first)
	}
	seen := map[int64]bool{}
	previous := ""
	for page := 1; page <= 3; page++ {
		p := read(fmt.Sprintf("page=%d", page))
		for _, item := range p.Items {
			if seen[item.ID] || item.ID > 205 || item.Directory < previous {
				t.Fatalf("duplicate, other batch, or split directory order: %+v", item)
			}
			seen[item.ID] = true
			previous = item.Directory
		}
	}
	if len(seen) != 205 {
		t.Fatal("missing files", len(seen))
	}
	last := read("page=9223372036854775807")
	if last.Page != 3 || len(last.Items) != 5 {
		t.Fatalf("last page: %+v", last)
	}
	selected := read("page=2&directory=" + url.QueryEscape("上海"))
	if selected.Total != 102 || len(selected.Items) != 2 || len(selected.Directories) != 3 {
		t.Fatalf("directory page: %+v", selected)
	}
	filtered := read("status=failed&directory=" + url.QueryEscape("上海"))
	if filtered.Total != 34 || filtered.AllTotal != 205 || filtered.FailedTotal != 68 {
		t.Fatalf("failure counts: %+v", filtered)
	}
	for _, item := range filtered.Items {
		if item.Status != "failed" || item.Directory != "上海" {
			t.Fatal(item)
		}
	}
	root := read("directory=")
	if root.Total != 1 || root.Items[0].ID != 205 {
		t.Fatalf("root filter: %+v", root)
	}
	named := read("directory=" + url.QueryEscape("上海"))
	if named.Items[0].NewName != "本批次名称.mp4" || named.Items[0].OriginalName != "clip-001.mp4" || named.Items[0].OperationStatus != "committed" {
		t.Fatal(named.Items[0])
	}
	failed := named.Items[1]
	if failed.ID != 3 || len(failed.Attempts) != 4 || failed.Attempts[3].Number != 4 || failed.NewName != "未提交名称.mp4" || failed.OperationStatus != "failed" {
		t.Fatal(failed)
	}
	for _, directory := range []string{"上", "上海 / 子目录", "%", "' OR 1=1 --"} {
		if p := read("directory=" + url.QueryEscape(directory)); p.Total != 0 || p.Page != 1 {
			t.Fatal("inexact directory filter", p)
		}
	}
	if _, err := a.DB.Exec(`UPDATE run_items SET status='failed' WHERE id=205`); err != nil {
		t.Fatal(err)
	}
	refreshed := read("status=failed&directory=")
	if refreshed.Total != 1 || refreshed.FailedTotal != 69 || refreshed.Items[0].ID != 205 {
		t.Fatal("live summary not updated", refreshed)
	}
	if _, err := a.DB.Exec(`DELETE FROM model_attempts; DELETE FROM run_items WHERE run=1`); err != nil {
		t.Fatal(err)
	}
	empty := read("status=failed&page=99")
	if empty.Total != 0 || empty.Page != 1 || len(empty.Items) != 0 || len(empty.Directories) != 0 {
		t.Fatal(empty)
	}
	for _, query := range []string{"status=invalid", "view=logs"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/runs/1/items?"+query, nil)
		r.SetPathValue("id", "1")
		a.items(w, r)
		if w.Code != 400 {
			t.Fatal("invalid query accepted", query, w.Code)
		}
	}
}

// Exercise the producer and HTTP reader together. Fabricated operation keys
// previously hid a mismatch with the suffix added by Library.AutoCommit.
func TestRunRecordsAfterAutomaticRenameAndRestore(t *testing.T) {
	root := t.TempDir()
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	tools := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	profile, err := (models.Store{DB: a.DB}).Get(1)
	if err != nil {
		t.Fatal(err)
	}
	profile.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test", Timeout: "2s", MaxTokens: 128}
	if _, err := (models.Store{DB: a.DB}).Save(profile); err != nil {
		t.Fatal(err)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	if err := (systemsettings.Store{DB: a.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "原视频.mp4"), []byte("isolated video fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	a.Jobs.Analyze = func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error) {
		return "本批次标题", nil
	}
	run, err := a.Jobs.Create(jobs.Request{Targets: []int64{1}, Key: "record-integration"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	a.Jobs.Execute(t.Context(), run)
	if _, err := os.Stat(filepath.Join(root, "本批次标题.mp4")); err != nil {
		t.Fatal(err)
	}
	var operation, entry int64
	if err := a.DB.QueryRow("SELECT id,entry FROM operations WHERE run=? AND status='committed'", run).Scan(&operation, &entry); err != nil {
		t.Fatal(err)
	}
	check := func(undone bool, current string) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", fmt.Sprintf("/api/runs/%d/items?page=1", run), nil)
		r.SetPathValue("id", fmt.Sprint(run))
		a.items(w, r)
		var result struct {
			Items []struct {
				Status          string `json:"status"`
				OriginalName    string `json:"original_name"`
				NewName         string `json:"new_name"`
				CurrentPath     string `json:"current_path"`
				OperationStatus string `json:"operation_status"`
				Undone          bool   `json:"undone"`
			} `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(result.Items) != 1 {
			t.Fatalf("unexpected records: %d %s", w.Code, w.Body.String())
		}
		item := result.Items[0]
		if item.Status != "done" || item.OriginalName != "原视频.mp4" || item.NewName != "本批次标题.mp4" || item.OperationStatus != "committed" || item.Undone != undone || item.CurrentPath != current {
			t.Fatalf("real rename result missing or replaced: %+v", item)
		}
	}
	check(false, "本批次标题.mp4")
	if _, err := a.Library.Restore(operation, "record-restore"); err != nil {
		t.Fatal(err)
	}
	check(true, "原视频.mp4")
	e, err := a.Library.Get(entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Library.Rename(entry, e.Version, "后续手动标题.mp4", "record-later-rename"); err != nil {
		t.Fatal(err)
	}
	check(true, "后续手动标题.mp4")
}
