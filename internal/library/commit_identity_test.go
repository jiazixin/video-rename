package library

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modernc.org/sqlite"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func init() {
	sqlite.MustRegisterScalarFunction("test_change_commit_file", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		path, mode := args[0].(string), args[1].(string)
		if mode == "parent" {
			dir := filepath.Dir(path)
			if err := os.Rename(dir, dir+".saved"); err != nil {
				return nil, err
			}
			if err := os.Mkdir(dir, 0o700); err != nil {
				return nil, err
			}
		}
		if mode == "replace" {
			if err := os.Rename(path, path+".saved"); err != nil {
				return nil, err
			}
		}
		return nil, os.WriteFile(path, []byte("externally changed content must survive"), 0o600)
	})
}

func TestCommitRejectsChangedFile(t *testing.T) {
	for _, operation := range []string{"manual", "automatic_same_name", "restore"} {
		for _, stage := range []string{"prepared", "finish"} {
			for _, change := range []string{"replace", "rewrite"} {
				t.Run(operation+"/"+stage+"/"+change, func(t *testing.T) {
					l, root, _ := fixture(t)
					put(t, filepath.Join(root, "original.mp4"))
					refresh(t, l, 1)
					e := entry(t, entryOptions{l: l, parent: 1, name: "original.mp4"})
					var undo int64
					target := "renamed.mp4"
					if operation == "restore" {
						o, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: "renamed.mp4", Key: "initial"})
						if err != nil {
							t.Fatal(err)
						}
						undo, target = o.ID, "original.mp4"
						e = entry(t, entryOptions{l: l, parent: 1, name: "renamed.mp4"})
					} else if operation == "automatic_same_name" {
						target = e.Name
					}
					source := filepath.Join(root, e.Name)
					snap, err := domain.StatSnapshot(source)
					if err != nil {
						t.Fatal(err)
					}
					mutated := source
					trigger := "BEFORE INSERT ON operations"
					if stage == "finish" {
						mutated = filepath.Join(root, target)
						trigger = "BEFORE UPDATE OF status ON operations WHEN NEW.status='committed'"
					}
					quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
					if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
						return tx.ExecContext(t.Context(), "CREATE TRIGGER change_leaf "+trigger+" BEGIN SELECT test_change_commit_file("+quote(mutated)+","+quote(change)+"); END")
					}); err != nil {
						t.Fatal(err)
					}
					var op Operation
					switch operation {
					case "manual":
						op, err = l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: target, Key: "changed-file"})
					case "automatic_same_name":
						op, err = l.AutoCommit(t.Context(), AutoCommitOptions{ID: e.ID, Snap: snap, Title: "original", Key: "changed-file", Run: 0})
					case "restore":
						op, err = l.Restore(t.Context(), undo, "changed-file")
					}
					if err == nil || !l.Halted() {
						t.Fatalf("must halt without success: %+v %v", op, err)
					}
					saved, err := l.Operation(t.Context(), op.ID)
					if err != nil || saved.Status != "prepared" {
						t.Fatalf("lost prepared evidence: %+v %v", saved, err)
					}
					var successes int
					if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
						return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM events WHERE operation=? AND category='success'", op.ID).Scan(&successes)
					}); err != nil || successes != 0 {
						t.Fatalf("false success: %d %v", successes, err)
					}
					if stage == "prepared" && source != filepath.Join(root, target) {
						if _, err := os.Stat(filepath.Join(root, target)); !errors.Is(err, fs.ErrNotExist) {
							t.Fatalf("changed source was moved: %v", err)
						}
					}
					if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "DROP TRIGGER change_leaf") }); err != nil {
						t.Fatal(err)
					}
					if err := l.Recover(t.Context()); err != nil {
						t.Fatal(err)
					}
					saved, err = l.Operation(t.Context(), op.ID)
					if err != nil || saved.Status != "review" {
						t.Fatalf("ambiguous operation must require review: %+v %v", saved, err)
					}
					content, err := os.ReadFile(mutated)
					if err != nil || string(content) != "externally changed content must survive" {
						t.Fatalf("external file lost: %q %v", content, err)
					}
					if change == "replace" {
						original, err := domain.StatSnapshot(mutated + ".saved")
						if err != nil || !original.Same(snap) {
							t.Fatalf("original changed: %+v %v", original, err)
						}
					}
				})
			}
		}
	}
}

func TestNumberingCommitRejectsChangedFile(t *testing.T) {
	for _, change := range []string{"replace", "rewrite", "parent"} {
		t.Run(change, func(t *testing.T) {
			l, root, batch := numberedFixture(t)
			item := batch.Items[0]
			path := filepath.Join(root, item.New)
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
			if err := l.db.Write(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), "CREATE TRIGGER change_numbering_leaf BEFORE UPDATE OF status ON image_numberings WHEN NEW.status='committed' BEGIN SELECT test_change_commit_file("+quote(path)+","+quote(change)+"); END")
				return err
			}); err != nil {
				t.Fatal(err)
			}
			result, err := l.ApplyNumbering(t.Context(), batch.ID, nil)
			if err == nil || result.Status != "review" {
				t.Fatalf("got status=%q err=%v, want review after external change during commit", result.Status, err)
			}
			saved, err := l.Numbering(t.Context(), batch.ID)
			if err != nil || saved.Status != "review" {
				t.Fatalf("got saved=%+v err=%v, want retained review journal", saved, err)
			}
			var successes int
			if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM events WHERE category='success'").Scan(&successes)
			}); err != nil || successes != 0 {
				t.Fatalf("got successes=%d err=%v, want no committed success", successes, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "externally changed content must survive" {
				t.Fatalf("got file=%q err=%v, want external content preserved", data, err)
			}
			if change == "replace" && !numberingMatch(path+".saved", item) {
				t.Fatal("got changed original snapshot, want original preserved after replacement")
			}
			if change == "parent" && !numberingMatch(filepath.Join(root+".saved", item.New), item) {
				t.Fatal("got changed original snapshot, want original preserved after parent replacement")
			}
		})
	}
}
