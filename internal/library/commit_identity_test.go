package library

import (
	"database/sql/driver"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modernc.org/sqlite"
	"video-rename/internal/domain"
)

func init() {
	sqlite.MustRegisterScalarFunction("test_change_commit_file", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		path, mode := args[0].(string), args[1].(string)
		if mode == "replace" {
			if err := os.Rename(path, path+".saved"); err != nil {
				return nil, err
			}
		}
		return nil, os.WriteFile(path, []byte("externally changed content must survive"), 0600)
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
					e := entry(t, l, 1, "original.mp4")
					var undo int64
					target := "renamed.mp4"
					if operation == "restore" {
						o, err := l.Rename(e.ID, e.Version, "renamed.mp4", "initial")
						if err != nil {
							t.Fatal(err)
						}
						undo, target = o.ID, "original.mp4"
						e = entry(t, l, 1, "renamed.mp4")
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
					if _, err := l.DB.Exec("CREATE TRIGGER change_leaf " + trigger + " BEGIN SELECT test_change_commit_file(" + quote(mutated) + "," + quote(change) + "); END"); err != nil {
						t.Fatal(err)
					}
					var op Operation
					switch operation {
					case "manual":
						op, err = l.Rename(e.ID, e.Version, target, "changed-file")
					case "automatic_same_name":
						op, err = l.AutoCommit(e.ID, snap, "original", "changed-file", 0)
					case "restore":
						op, err = l.Restore(undo, "changed-file")
					}
					if err == nil || !l.Halted() {
						t.Fatalf("must halt without success: %+v %v", op, err)
					}
					saved, err := l.Operation(op.ID)
					if err != nil || saved.Status != "prepared" {
						t.Fatalf("lost prepared evidence: %+v %v", saved, err)
					}
					var successes int
					if err := l.DB.QueryRow("SELECT count(*) FROM events WHERE operation=? AND category='success'", op.ID).Scan(&successes); err != nil || successes != 0 {
						t.Fatalf("false success: %d %v", successes, err)
					}
					if stage == "prepared" && source != filepath.Join(root, target) {
						if _, err := os.Stat(filepath.Join(root, target)); !os.IsNotExist(err) {
							t.Fatalf("changed source was moved: %v", err)
						}
					}
					if _, err := l.DB.Exec("DROP TRIGGER change_leaf"); err != nil {
						t.Fatal(err)
					}
					if err := l.Recover(); err != nil {
						t.Fatal(err)
					}
					saved, err = l.Operation(op.ID)
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
