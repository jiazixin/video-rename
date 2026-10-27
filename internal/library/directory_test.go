package library

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modernc.org/sqlite"

	"video-rename/internal/domain"
)

func init() {
	// Inject an external directory replacement precisely after validation,
	// while the operation journal is being written, without production hooks.
	sqlite.MustRegisterScalarFunction("test_swap_directory", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		parent, outside := args[0].(string), args[1].(string)
		if err := os.Rename(parent, parent+"-saved"); err != nil {
			return nil, err
		}
		return nil, os.Symlink(outside, parent)
	})
}

func TestRenameParentReplacementDoesNotRedirectCommit(t *testing.T) {
	for _, numbering := range []bool{false, true} {
		t.Run(fmt.Sprintf("numbering=%v", numbering), func(t *testing.T) {
			l, root, _ := fixture(t)
			parent, outside := filepath.Join(root, "folder"), t.TempDir()
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			name := "source.mp4"
			if numbering {
				name = "source.jpg"
			}
			put(t, filepath.Join(parent, name))
			put(t, filepath.Join(outside, name))
			refresh(t, l, 1)
			dir := entry(t, l, 1, "folder")
			refresh(t, l, dir.ID)
			e := entry(t, l, dir.ID, name)
			var batch Numbering
			var err error
			trigger := "BEFORE INSERT ON operations"
			if numbering {
				batch, err = l.PreviewNumbering(t.Context(), NumberingRequest{Parent: dir.ID, Media: "image", Sort: "name_asc", Settings: domain.DefaultNumberingSettings()})
				if err != nil {
					t.Fatal(err)
				}
				trigger = "BEFORE UPDATE OF pending_name ON image_numbering_items WHEN NEW.pending_name<>''"
			}
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
			if _, err := l.DB.Exec("CREATE TRIGGER swap_parent " + trigger + " BEGIN SELECT test_swap_directory(" + quote(parent) + "," + quote(outside) + "); END"); err != nil {
				t.Fatal(err)
			}
			if numbering {
				_, err = l.ApplyNumbering(t.Context(), batch.ID, nil)
			} else {
				_, err = l.Rename(t.Context(), e.ID, e.Version, "renamed.mp4", "parent-swap")
			}
			if err == nil {
				t.Fatal("replaced hierarchy must require review, not report success")
			}
			files, err := os.ReadDir(outside)
			if err != nil || len(files) != 1 || files[0].Name() != name {
				t.Fatalf("outside directory mutated: %v, %v", files, err)
			}
			if _, err := l.openDirectory(dir.ID); err == nil {
				t.Fatal("accepted replacement symlink")
			}
		})
	}
}

func TestAutoCommitAllocatesSuffixForHardLink(t *testing.T) {
	for _, target := range []string{"title.mp4", "SOURCE.mp4"} {
		t.Run(target, func(t *testing.T) {
			l, root, _ := fixture(t)
			source := filepath.Join(root, "source.mp4")
			put(t, source)
			if err := os.Link(source, filepath.Join(root, target)); err != nil {
				if errors.Is(err, fs.ErrExist) && target == "SOURCE.mp4" {
					t.Skip("filesystem cannot hold separate case-only names")
				}
				t.Fatal(err)
			}
			refresh(t, l, 1)
			e := entry(t, l, 1, "source.mp4")
			snap, err := domain.StatSnapshot(source)
			if err != nil {
				t.Fatal(err)
			}
			title := strings.TrimSuffix(target, ".mp4")
			op, err := l.AutoCommit(t.Context(), e.ID, snap, title, "hardlink", 0)
			if err != nil || op.New != title+" (2).mp4" {
				t.Fatalf("commit: %+v, %v", op, err)
			}
			var count int
			if err := l.DB.QueryRow("SELECT count(*) FROM operations").Scan(&count); err != nil || count != 1 {
				t.Fatalf("unnecessary retries: %d, %v", count, err)
			}
			if _, err := os.Stat(filepath.Join(root, target)); err != nil {
				t.Fatal("existing hardlink lost:", err)
			}
		})
	}
}
