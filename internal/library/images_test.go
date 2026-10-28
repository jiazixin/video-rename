package library

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestAdjacentImagesWrapAndSkipVideos(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"a.png", "b.mp4", "c.JPG", "d.webm", "e.gif"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var id int64
	l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT id FROM entries WHERE name='a.png'").Scan(&id)
	})
	for _, name := range []string{"c.JPG", "e.gif", "a.png"} {
		e, err := l.AdjacentImage(t.Context(), id, false)
		if err != nil || e.Name != name {
			t.Fatal(e, err)
		}
		id = e.ID
	}
	e, err := l.AdjacentImage(t.Context(), id, true)
	if err != nil || e.Name != "e.gif" {
		t.Fatal(e, err)
	}
	l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT id FROM entries WHERE name='b.mp4'").Scan(&id)
	})
	if _, err = l.AdjacentImage(t.Context(), id, false); err == nil {
		t.Fatal("video accepted")
	}
}
