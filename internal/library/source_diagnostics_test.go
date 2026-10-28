package library

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func TestAncestorIdentityDiagnostic(t *testing.T) {
	l, root, _ := fixture(t)
	dir := filepath.Join(root, "folder")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	parent := entry(t, entryOptions{l: l, parent: 1, name: "folder"})
	refresh(t, l, parent.ID)
	file := entry(t, entryOptions{l: l, parent: parent.ID, name: "a.mp4"})
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "UPDATE entries SET identity='999:888' WHERE id=?", parent.ID)
	}); err != nil {
		t.Fatal(err)
	}
	_, err := l.Path(t.Context(), file.ID)
	diagnostic, ok := errors.AsType[*domain.JobError](err)
	if !ok {
		t.Fatalf("expected source diagnostic, got %v", err)
	}
	for _, want := range []string{dir, "999 ->", "888 ->", "kind: directory", "stage: " + domain.StagePathIdentity} {
		if !strings.Contains(diagnostic.ProcessingLog, want) {
			t.Fatalf("missing %q: %s", want, diagnostic.ProcessingLog)
		}
	}
}
