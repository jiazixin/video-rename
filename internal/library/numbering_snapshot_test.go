package library

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNumberingSnapshotDiagnosesNonParticipatingEntries(t *testing.T) {
	l, root, b := numberedFixture(t)
	original := filepath.Join(root, "video.MP4")
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.txt"), []byte("new"), 0600); err != nil {
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
	persisted, err := l.Numbering(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var detail strings.Builder
	if !describeNumberingSnapshotDiff(&detail, persisted.Fingerprint, actual) {
		t.Fatal("snapshot not persisted")
	}
	for _, want := range []string{"video.MP4", "当前目录列表中缺失", "added.txt", "新增条目", "总计 2 个"} {
		if !strings.Contains(detail.String(), want) {
			t.Fatal(want, detail.String())
		}
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
	for _, want := range []string{"photo.jpg", "1:100 → 1:999", "大小 42 → 42", "总计 1 个"} {
		if !strings.Contains(detail.String(), want) {
			t.Fatal(want, detail.String())
		}
	}
}
