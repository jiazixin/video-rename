package library

import (
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func TestNumberingSnapshotDiagnosesNonParticipatingEntries(t *testing.T) {
	l, root, b := numberedFixture(t)
	original := filepath.Join(root, "video.MP4")
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := directoryFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	// Diagnostics must use the failing observation even if the files change again.
	if err := os.Remove(filepath.Join(root, "added.txt")); err != nil {
		t.Fatal(err)
	}
	persisted, err := l.Numbering(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var detail strings.Builder
	if !describeNumberingSnapshotDiff(&detail, persisted.Fingerprint, actual) {
		t.Fatal("snapshot not persisted")
	}
	for _, want := range []string{"video.MP4", "missing from the current listing", "added.txt", "new entry", "2 entries differ"} {
		if !strings.Contains(detail.String(), want) {
			t.Fatal(want, detail.String())
		}
	}
}

func TestNumberingRejectsInvalidPreviewFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"plain", "fingerprint"},
		{"malformed", "{"},
		{"unsupported_version", `{"version":2,"hash":"fingerprint","entries":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, root, b := numberedFixture(t)
			before, err := directoryFingerprint(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.DB.Exec("UPDATE image_numberings SET fingerprint=? WHERE id=?", tc.raw, b.ID); err != nil {
				t.Fatal(err)
			}
			_, err = l.ApplyNumbering(t.Context(), b.ID, nil)
			diagnostic, ok := errors.AsType[*domain.JobError](err)
			if !ok || diagnostic.Code != domain.CodeSourceChanged || diagnostic.Details["reason"] != "preview_invalid" {
				t.Fatal("invalid preview accepted or misdiagnosed", err)
			}
			after, err := directoryFingerprint(root)
			if err != nil || after != before {
				t.Fatal("invalid preview changed files", err)
			}
			saved, err := l.Numbering(t.Context(), b.ID)
			if err != nil || saved.Status != "preview" || saved.Fingerprint != tc.raw {
				t.Fatal("invalid preview was rewritten", saved, err)
			}
			var operations int
			if err := l.DB.QueryRow("SELECT count(*) FROM operations").Scan(&operations); err != nil || operations != 0 {
				t.Fatal("invalid preview created file operations", operations, err)
			}
		})
	}
}
func TestNumberingSnapshotReportsIdentityOnlyDrift(t *testing.T) {
	before := numberingDirectorySnapshot{Version: 3, Hash: "old", Entries: []numberingEntrySnapshot{{Name: "photo.jpg", Type: "----------", Identity: "1:100", Size: 42, Mtime: "2026-09-27T00:00:00Z"}}}
	old, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	before.Hash = "new"
	before.Entries[0].Identity = "1:999"
	current, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var detail strings.Builder
	if !describeNumberingSnapshotDiff(&detail, string(old), string(current)) {
		t.Fatal("not decoded")
	}
	for _, want := range []string{"photo.jpg", "1:100 -> 1:999", "size 42 -> 42", "1 entries differ"} {
		if !strings.Contains(detail.String(), want) {
			t.Fatal(want, detail.String())
		}
	}
}
