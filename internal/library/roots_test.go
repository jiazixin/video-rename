package library

import (
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func TestBindRootsToleratesRemountDeviceChangesAndRejectsInvalidMounts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	const (
		color1 = "/mnt/color/color1"
		color2 = "/mnt/color/color2"
	)
	now := time.Unix(1790000000, 0)
	initial := map[string]rootObservation{
		color1: {snap: domain.Snapshot{Dev: 51, Ino: 25955785880, Size: 4096, ModTime: now}, mountPoint: true},
		color2: {snap: domain.Snapshot{Dev: 52, Ino: 131, Size: 4096, ModTime: now}, mountPoint: true},
	}
	inspectMap := func(m map[string]rootObservation) func(string) (rootObservation, error) {
		return func(path string) (rootObservation, error) {
			obs, ok := m[path]
			if !ok {
				return rootObservation{}, os.ErrNotExist
			}
			return obs, nil
		}
	}

	first, err := bindRootsWith(ctx, db, []string{color1, color2}, inspectMap(initial))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != 1 || first[1].ID != 2 || first[0].dev != 51 || first[1].dev != 52 {
		t.Fatalf("got initial roots %+v", first)
	}

	// Simulate remounting both CIFS shares: color1 gets color2's former dev (52)
	// and color2 gets a new dev (58), while server inodes and mount state match.
	remounted := map[string]rootObservation{
		color1: {snap: domain.Snapshot{Dev: 52, Ino: 25955785880, Size: 4096, ModTime: now}, mountPoint: true},
		color2: {snap: domain.Snapshot{Dev: 58, Ino: 131, Size: 4096, ModTime: now}, mountPoint: true},
	}
	second, err := bindRootsWith(ctx, db, []string{color1, color2}, inspectMap(remounted))
	if err != nil {
		t.Fatalf("remount with new st_dev rejected: %v", err)
	}
	if len(second) != 2 || second[0].ID != 1 || second[1].ID != 2 || second[0].dev != 52 || second[1].dev != 58 {
		t.Fatalf("got remounted roots %+v", second)
	}

	// Verify stored root identities exclude st_dev.
	var stored1, stored2 string
	if err := db.View(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT identity FROM entries WHERE id=1").Scan(&stored1); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT identity FROM entries WHERE id=2").Scan(&stored2)
	}); err != nil {
		t.Fatal(err)
	}
	if stored1 != "r1:25955785880" || stored2 != "r2:131" {
		t.Fatalf("got stored identities %q and %q, want r1:25955785880 and r2:131", stored1, stored2)
	}

	// Unmounted local empty directory: even with a coincidental inode match,
	// losing mount_point status must be rejected.
	unmounted := map[string]rootObservation{
		color1: {snap: domain.Snapshot{Dev: 2049, Ino: 25955785880, Size: 4096, ModTime: now}, mountPoint: false},
		color2: remounted[color2],
	}
	if _, err := bindRootsWith(ctx, db, []string{color1, color2}, inspectMap(unmounted)); err == nil || !strings.Contains(err.Error(), "mount state changed") {
		t.Fatalf("got %v, want mount state changed error", err)
	}

	// Changed root inode (e.g. serverino lost / mounted with noserverino or wrong share)
	// must be rejected with expected and actual identities.
	changedIno := map[string]rootObservation{
		color1: remounted[color1],
		color2: {snap: domain.Snapshot{Dev: 58, Ino: 99999, Size: 4096, ModTime: now}, mountPoint: true},
	}
	if _, err := bindRootsWith(ctx, db, []string{color1, color2}, inspectMap(changedIno)); err == nil || !strings.Contains(err.Error(), "r2:131") || !strings.Contains(err.Error(), "r2:99999") {
		t.Fatalf("got %v, want identity mismatch diagnostics", err)
	}

	// Removing a previously bound root from media_dir must be rejected.
	if _, err := bindRootsWith(ctx, db, []string{color1}, inspectMap(remounted)); err == nil || !strings.Contains(err.Error(), "was removed from media_dir") {
		t.Fatalf("got %v, want removed root error", err)
	}

	// Two configured paths resolving to the same runtime (dev, ino) are rejected.
	duplicate := map[string]rootObservation{
		color1: {snap: domain.Snapshot{Dev: 52, Ino: 131, Size: 4096, ModTime: now}, mountPoint: true},
		color2: {snap: domain.Snapshot{Dev: 52, Ino: 131, Size: 4096, ModTime: now}, mountPoint: true},
	}
	if _, err := bindRootsWith(ctx, db, []string{color1, color2}, inspectMap(duplicate)); err == nil || !strings.Contains(err.Error(), "duplicate media directory") {
		t.Fatalf("got %v, want duplicate media directory error", err)
	}

	// Two distinct mounts that share the same root inode (e.g. XFS root 128 on
	// separate disks) must be allowed because their runtime devices differ.
	sameInoDB, err := storage.Open(ctx, filepath.Join(t.TempDir(), "same-ino.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sameInoDB.Close(); err != nil {
			t.Error(err)
		}
	})
	sameIno := map[string]rootObservation{
		color1: {snap: domain.Snapshot{Dev: 51, Ino: 128, Size: 4096, ModTime: now}, mountPoint: true},
		color2: {snap: domain.Snapshot{Dev: 52, Ino: 128, Size: 4096, ModTime: now}, mountPoint: true},
	}
	sameRoots, err := bindRootsWith(ctx, sameInoDB, []string{color1, color2}, inspectMap(sameIno))
	if err != nil || len(sameRoots) != 2 || sameRoots[0].ID == sameRoots[1].ID {
		t.Fatalf("two mounts with equal root ino on different devices failed: %+v %v", sameRoots, err)
	}
}

func TestRuntimeRootDeviceChangeBlocksOperationsAndCrossDeviceEntriesStayOther(t *testing.T) {
	t.Parallel()
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "clip.mp4"))
	refresh(t, l, 1)
	file := entry(t, entryOptions{l: l, parent: 1, name: "clip.mp4"})

	snap, err := domain.StatSnapshot(filepath.Join(root, "clip.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if !l.MatchesPathIdentity(filepath.Join(root, "clip.mp4"), snap, file.Identity) {
		t.Fatal("MatchesPathIdentity rejected valid indexed file")
	}
	wrongDev := snap
	wrongDev.Dev++
	if l.MatchesPathIdentity(filepath.Join(root, "clip.mp4"), wrongDev, file.Identity) {
		t.Fatal("MatchesPathIdentity accepted snapshot from another runtime device")
	}

	// Simulate the root mount changing device mid-process: Path, ScanDirectory,
	// OpenPlayback and mutations must all reject the swapped mount.
	original := l.byRootID[1]
	swapped := original
	swapped.dev++
	l.byRootID[1] = swapped
	l.roots[0] = swapped
	if _, err := l.Path(t.Context(), file.ID); !errors.Is(err, errRootUnavailable) {
		t.Fatalf("Path got %v, want errRootUnavailable", err)
	}
	if _, err := l.ScanDirectory(t.Context(), 1); !errors.Is(err, errRootUnavailable) {
		t.Fatalf("ScanDirectory got %v, want errRootUnavailable", err)
	}
	if f, err := l.OpenPlayback(t.Context(), file.ID); !errors.Is(err, errRootUnavailable) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("OpenPlayback got %v, want errRootUnavailable", err)
	}
	if _, err := l.Rename(t.Context(), RenameRequest{ID: file.ID, Version: file.Version, Name: "moved.mp4", Key: "swapped-dev"}); !errors.Is(err, errRootUnavailable) {
		t.Fatalf("Rename got %v, want errRootUnavailable", err)
	}

	// Restore runtime root and confirm cross-device child inspection classifies as "other".
	l.byRootID[1] = original
	l.roots[0] = original
	sub := filepath.Join(root, "nested")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	dirFile, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dirFile.Close() })
	entries, err := dirFile.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	var nestedEntry os.DirEntry
	for _, de := range entries {
		if de.Name() == "nested" {
			nestedEntry = de
			break
		}
	}
	if nestedEntry == nil {
		t.Fatal("missing nested directory entry")
	}
	otherRoot := original
	otherRoot.dev++
	scanned, err := scanPinnedDirectoryEntry(otherRoot, dirFile, nestedEntry)
	if err != nil {
		t.Fatal(err)
	}
	if scanned.kind != "other" || !strings.HasPrefix(scanned.identity, "xdev:") {
		t.Fatalf("cross-device child got %+v, want kind=other with xdev identity", scanned)
	}
}

func TestReplacedRootDirectoryRejectedOnRestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	base := t.TempDir()
	root := filepath.Join(base, "videos")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dbpath := filepath.Join(base, "state", "data.sqlite")
	db, err := storage.Open(ctx, dbpath)
	if err != nil {
		t.Fatal(err)
	}
	l, err := New(ctx, db, []string{root}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Keep the old directory alive so its inode cannot be recycled for the replacement.
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, dbpath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := New(ctx, db, []string{root}, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("replaced root got %v, want identity changed error", err)
	}
}

func TestInspectRootRejectsUncleanedPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, bad := range []string{dir + string(os.PathSeparator), dir + "/.", "relative/path"} {
		if _, err := inspectRoot(bad); err == nil {
			t.Fatalf("inspectRoot(%q) succeeded, want invalid media directory error", bad)
		}
	}
}

func TestPersistedIdentitiesExcludeDeviceAndSurviveRemount(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	base := t.TempDir()
	root := filepath.Join(base, "videos")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "clip.mp4"))
	put(t, filepath.Join(root, "pic.jpg"))

	dbpath := filepath.Join(base, "state", "data.sqlite")
	db, err := storage.Open(ctx, dbpath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	l, err := New(ctx, db, []string{root}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)

	clip := entry(t, entryOptions{l: l, parent: 1, name: "clip.mp4"})
	if res := l.SetProcessed(ctx, []int64{clip.ID}, true); res.Changed != 1 || res.Failed != 0 {
		t.Fatalf("SetProcessed got %+v, want 1 changed and 0 failed", res)
	}
	op, err := l.Rename(ctx, RenameRequest{ID: clip.ID, Version: clip.Version, Name: "renamed.mp4", Key: "persist-rename"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := l.PreviewNumbering(ctx, NumberingRequest{Parent: 1, Media: "image", Sort: "name_asc", Settings: domain.DefaultNumberingSettings()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ApplyNumbering(ctx, batch.ID, nil); err != nil {
		t.Fatal(err)
	}
	l.Close()

	// Verify every persisted identity across entries, marks, operations and
	// numbering items uses root-scoped r<rootID>:<ino> rather than st_dev:ino.
	tables := []string{"entries", "marks", "operations", "image_numbering_items"}
	for _, table := range tables {
		var count int
		if err := db.View(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, "SELECT identity FROM "+table)
			if err != nil {
				return err
			}
			ids, err := storage.CollectRows(rows, func(r *sql.Rows) (s string, err error) {
				err = r.Scan(&s)
				return s, err
			})
			if err != nil {
				return err
			}
			count = len(ids)
			for _, id := range ids {
				rootID, _, ok := parseEntryIdentity(id)
				if !ok || rootID != 1 {
					t.Errorf("%s stored unexpected identity %q", table, id)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("%s has no rows to verify", table)
		}
	}

	// Simulate remounting the same directory with a new kernel st_dev.
	realObs, err := inspectRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	shifted := realObs
	shifted.snap.Dev += 99
	rebound, err := bindRootsWith(ctx, db, []string{root}, func(string) (rootObservation, error) {
		return shifted, nil
	})
	if err != nil {
		t.Fatalf("bindRootsWith with shifted st_dev failed: %v", err)
	}
	if len(rebound) != 1 || rebound[0].ID != 1 || rebound[0].dev != shifted.snap.Dev {
		t.Fatalf("got rebound roots %+v", rebound)
	}

	// Reopen normally and confirm marks and restore history remain usable.
	l2, err := New(ctx, db, []string{root}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l2.Close)
	reopenedClip := entry(t, entryOptions{l: l2, parent: 1, name: "renamed.mp4"})
	if reopenedClip.Processed == "" {
		t.Fatal("processed mark lost after reopen")
	}
	if _, err := l2.Restore(ctx, op.ID, "persist-restore"); err != nil {
		t.Fatalf("restore after reopen failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "clip.mp4")); err != nil {
		t.Fatalf("restored file missing: %v", err)
	}
}
