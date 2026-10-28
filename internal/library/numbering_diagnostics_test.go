package library

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func TestNumberingExecutionDiagnosticSurvivesRollbackAndHistory(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"a.JPG", "b.JPG"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	preview, err := l.PreviewNumbering(t.Context(), NumberingRequest{Parent: 1, Media: "image", Sort: "name_asc", Settings: domain.DefaultNumberingSettings()})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := l.ApplyNumbering(t.Context(), preview.ID, func(done, total int) {
		if done == 1 {
			if err := os.WriteFile(filepath.Join(root, "2.jpg"), []byte("external occupant"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	})
	diagnostic, ok := errors.AsType[*domain.JobError](err)
	cause, _ := diagnostic.Details["cause"].(domain.Record)
	if !ok || diagnostic.Code != "numbering_rolled_back" || cause.Code != string(domain.CodeRenameConflict) || batch.Status != "rolled_back" {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	for _, want := range []string{"phase: final_rename", "original name: \"b.JPG\"", "target name: \"2.jpg\"", "target \"2.jpg\": kind=file", "entries matching the target: 1"} {
		if !strings.Contains(diagnostic.ProcessingLog, want) {
			t.Fatalf("missing %q in %s", want, diagnostic.ProcessingLog)
		}
	}
	assertOriginals(t, root, preview)
	data, err := os.ReadFile(filepath.Join(root, "2.jpg"))
	if err != nil || string(data) != "external occupant" {
		t.Fatalf("occupant changed: %q %v", data, err)
	}
	saved, err := l.Numbering(t.Context(), preview.ID)
	if err != nil || saved.ProcessingLog != diagnostic.ProcessingLog || saved.Error.Code != batch.Error.Code || saved.Error.Message != batch.Error.Message {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	history, err := l.NumberingHistory(t.Context(), 1, "image")
	if err != nil || len(history) != 1 || history[0].ProcessingLog != diagnostic.ProcessingLog {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	tree, err := l.NumberingTreeHistory(t.Context(), 1)
	if err != nil || len(tree) != 1 || tree[0].ProcessingLog != diagnostic.ProcessingLog {
		t.Fatalf("tree=%+v err=%v", tree, err)
	}
}

func TestNumberingTemporaryMoveDiagnostic(t *testing.T) {
	l, root, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(root, "photo.JPG"), []byte("photo.JPG"), 0o600); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	batch, err := l.PreviewNumbering(t.Context(), NumberingRequest{Parent: 1, Media: "image", Sort: "name_asc", Settings: domain.DefaultNumberingSettings()})
	if err != nil {
		t.Fatal(err)
	}
	item := &batch.Items[0]
	put(t, filepath.Join(root, item.Temp))
	err = l.moveNumbering(t.Context(), moveNumberingOptions{Batch: &batch, Item: item, Path: root, Target: item.Temp})
	diagnostic, ok := errors.AsType[*domain.JobError](err)
	if !ok || !strings.Contains(diagnostic.ProcessingLog, "phase: temporary_rename") || !strings.Contains(diagnostic.ProcessingLog, item.Temp) {
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
	for _, original := range []Numbering{
		{Error: domain.Record{Code: "target_exists", Message: "target already exists"}},
		{Error: domain.Record{Code: "numbering_rolled_back", Message: "rolled back", Details: map[string]any{"media": "image"}}},
		{Error: domain.Record{Code: "numbering_recovery_incomplete", Message: "recovery incomplete"}, ProcessingLog: "evidence\nwith \"quotes\""},
	} {
		var loaded Numbering
		if err := loaded.decodeNumberingError(original.numberingErrorRecord()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(loaded.Error, original.Error) || loaded.ProcessingLog != original.ProcessingLog {
			t.Fatalf("%+v", loaded)
		}
	}
	for _, stored := range []string{`{"message":"recovery incomplete","processing_log":"evidence"}`, `plain text`} {
		var invalid Numbering
		if err := invalid.decodeNumberingError(stored); err == nil {
			t.Fatal("accepted error record without a code", stored)
		}
	}

}
