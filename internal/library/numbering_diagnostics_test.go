package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func TestNumberingExecutionDiagnosticSurvivesRollbackAndHistory(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"a.JPG", "b.JPG"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	preview, err := l.PreviewNumberingContext(t.Context(), 1, "image", "", "name_asc", domain.DefaultNumberingSettings())
	if err != nil {
		t.Fatal(err)
	}
	batch, err := l.ApplyNumberingContext(t.Context(), preview.ID, func(done, total int) {
		if done == 1 {
			if err := os.WriteFile(filepath.Join(root, "2.jpg"), []byte("external occupant"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	})
	diagnostic, ok := errors.AsType[*domain.JobError](err)
	if !ok || diagnostic.Code != domain.CodeRenameConflict || batch.Status != "rolled_back" {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	for _, want := range []string{"阶段：最终编号", "原文件名：\"b.JPG\"", "目标文件名：\"2.jpg\"", "目标路径 \"2.jpg\"：类型=file", "目标同名或大小写匹配条目总数：1"} {
		if !strings.Contains(diagnostic.ProcessingLog, want) {
			t.Fatalf("missing %q in %s", want, diagnostic.ProcessingLog)
		}
	}
	assertOriginals(t, root, preview)
	data, err := os.ReadFile(filepath.Join(root, "2.jpg"))
	if err != nil || string(data) != "external occupant" {
		t.Fatalf("occupant changed: %q %v", data, err)
	}
	saved, err := l.Numbering(preview.ID)
	if err != nil || saved.ProcessingLog != diagnostic.ProcessingLog || saved.Error != batch.Error {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	history, err := l.NumberingHistory(1, "image")
	if err != nil || len(history) != 1 || history[0].ProcessingLog != diagnostic.ProcessingLog {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	tree, err := l.NumberingTreeHistory(1)
	if err != nil || len(tree) != 1 || tree[0].ProcessingLog != diagnostic.ProcessingLog {
		t.Fatalf("tree=%+v err=%v", tree, err)
	}
}

func TestNumberingTemporaryMoveDiagnostic(t *testing.T) {
	l, root, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(root, "photo.JPG"), []byte("photo.JPG"), 0600); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	batch, err := l.PreviewNumberingContext(t.Context(), 1, "image", "", "name_asc", domain.DefaultNumberingSettings())
	if err != nil {
		t.Fatal(err)
	}
	item := &batch.Items[0]
	put(t, filepath.Join(root, item.Temp))
	err = l.moveNumbering(&batch, item, root, item.Temp)
	diagnostic, ok := errors.AsType[*domain.JobError](err)
	if !ok || !strings.Contains(diagnostic.ProcessingLog, "阶段：临时改名") || !strings.Contains(diagnostic.ProcessingLog, item.Temp) {
		t.Fatalf("%v", err)
	}
	for name, want := range map[string]string{"photo.JPG": "photo.JPG", item.Temp: "video fixture"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s: %q %v", name, data, err)
		}
	}
}

func TestNumberingErrorRecordPlainAndStructured(t *testing.T) {
	for _, original := range []Numbering{{Error: "目标已存在"}, {Error: "恢复未完成", ProcessingLog: "现场\n含中文与引号\""}} {
		loaded := Numbering{Error: original.numberingErrorRecord()}
		loaded.decodeNumberingError()
		if loaded.Error != original.Error || loaded.ProcessingLog != original.ProcessingLog {
			t.Fatalf("%+v", loaded)
		}
	}
}
