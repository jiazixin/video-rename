package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/rename"
)

func TestNumberingRules(t *testing.T) {
	cases := []struct {
		name               string
		old, outside, want []string
	}{
		{"duplicates and mixed formats", []string{"1.JPG", "1.PNG", "风景.jpg", "3.WEBP", "截图.PNG"}, nil, []string{"1.jpg", "4.png", "5.jpg", "3.webp", "6.png"}},
		{"gaps", []string{"1.jpg", "5.png", "x.gif"}, nil, []string{"1.jpg", "5.png", "6.gif"}},
		{"leading zeros and zero", []string{"001.JPG", "1.png", "0.png", "2abc.jpg"}, nil, []string{"1.jpg", "2.png", "3.png", "4.jpg"}},
		{"hidden numbers", []string{"x.jpg", "1.PNG"}, []string{"1.jpg", "10.webp"}, []string{"11.jpg", "12.png"}},
		{"large numbers", []string{"999999999999999999999999.png", "x.jpg"}, nil, []string{"999999999999999999999999.png", "1000000000000000000000000.jpg"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var selected, outside []Entry
			for _, s := range tc.old {
				selected = append(selected, Entry{Name: s})
			}
			for _, s := range tc.outside {
				outside = append(outside, Entry{Name: s})
			}
			rules := domain.DefaultMediaNumberingRules()
			rules.Compact = false
			got := numberingNames(selected, outside, nil, rules, "")
			for i, g := range got {
				if g.New != tc.want[i] {
					t.Fatalf("%s => %s; want %s", g.Old, g.New, tc.want[i])
				}
			}
		})
	}
}
func numberedFixture(t *testing.T) (*Library, string, Numbering) {
	t.Helper()
	l, root, _ := fixture(t)
	for _, name := range []string{"1.JPG", "1.PNG", "3.WEBP", "风景.jpg", "截图.PNG", "video.MP4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "folder", "nested.JPG"))
	refresh(t, l, 1)
	b, err := l.PreviewNumbering(1, "image", "", "name_asc", domain.DefaultNumberingSettings())
	if err != nil {
		t.Fatal(err)
	}
	return l, root, b
}
func TestAudioNumberingScopesToAudioFiles(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"song.MP3", "1.mp3", "pic.jpg", "clip.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	if _, err := l.PreviewNumbering(1, "video", "", "name_asc", domain.DefaultNumberingSettings()); err == nil {
		t.Fatal("invalid media accepted")
	}
	b, err := l.PreviewNumbering(1, "audio", "", "name_asc", domain.DefaultNumberingSettings())
	if err != nil {
		t.Fatal(err)
	}
	if b.Total != 2 || b.Changed != 1 {
		t.Fatal(b)
	}
	if b.Items[0].Old != "1.mp3" || b.Items[0].New != "1.mp3" || b.Items[1].Old != "song.MP3" || b.Items[1].New != "2.mp3" {
		t.Fatal(b.Items)
	}
	if _, err = l.ApplyNumbering(b.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pic.jpg", "clip.mp4", "1.mp3", "2.mp3"} {
		if _, err = os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	undo, err := l.PreviewNumberingRestore(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.ApplyNumbering(undo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "song.MP3")); err != nil {
		t.Fatal(err)
	}
	images, err := l.PreviewNumbering(1, "image", "", "name_asc", domain.DefaultNumberingSettings())
	if err != nil || images.Total != 1 || images.Items[0].Old != "pic.jpg" {
		t.Fatal(images, err)
	}
}
func TestNumberingRuleOptions(t *testing.T) {
	entries := func(names ...string) []Entry {
		list := make([]Entry, len(names))
		for i, n := range names {
			list[i] = Entry{Name: n}
		}
		return list
	}
	names := func(selected, outside, peers []Entry, r domain.MediaNumberingRules, peerPrefix string) []string {
		items := numberingNames(selected, outside, peers, r, peerPrefix)
		got := make([]string, len(items))
		for i, item := range items {
			got[i] = item.New
		}
		return got
	}
	// Padding pads kept and fresh numbers alike.
	padded := names(entries("5.jpg", "x.PNG"), nil, nil, domain.MediaNumberingRules{Padding: 3, Lowercase: true}, "")
	if padded[0] != "005.jpg" || padded[1] != "006.png" {
		t.Fatal(padded)
	}
	// Uppercase extensions survive when lowercase is off.
	kept := names(entries("1.JPG"), nil, nil, domain.MediaNumberingRules{Padding: 1, Lowercase: false}, "")
	if kept[0] != "1.JPG" {
		t.Fatal(kept)
	}
	// Only prefix+digits names count as numbered once a prefix is set.
	prefixed := names(entries("IMG_2.JPG", "2.png", "plain.jpg"), nil, nil, domain.MediaNumberingRules{Padding: 2, Lowercase: true, Prefix: "IMG_"}, "")
	if prefixed[0] != "IMG_02.jpg" || prefixed[1] != "IMG_03.png" || prefixed[2] != "IMG_04.jpg" {
		t.Fatal(prefixed)
	}
	// Compact mode renumbers from 1, stepping around reserved numbers.
	compact := names(entries("9.jpg", "1.png", "x.gif"), entries("2.jpg"), nil, domain.MediaNumberingRules{Padding: 1, Lowercase: true, Compact: true}, "")
	if compact[0] != "1.jpg" || compact[1] != "3.png" || compact[2] != "4.gif" {
		t.Fatal(compact)
	}
	// A shared sequence reserves peer numbers parsed with the peer prefix.
	shared := names(entries("a.jpg"), nil, entries("AUD_7.mp3"), domain.MediaNumberingRules{Padding: 1, Lowercase: true}, "AUD_")
	if shared[0] != "8.jpg" {
		t.Fatal(shared)
	}
}

func TestNumberingSharedSequenceAcrossClasses(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"1.jpg", "7.mp3", "song.mp3", "pic.jpg"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	settings := domain.DefaultNumberingSettings()
	settings.Image.Compact = false
	settings.Audio.Compact = false
	settings.Shared = true
	b, err := l.PreviewNumbering(1, "image", "", "name_asc", settings)
	if err != nil {
		t.Fatal(err)
	}
	if b.Total != 2 || b.Items[len(b.Items)-1].New != "8.jpg" {
		t.Fatal(b.Items)
	}
	audio, err := l.PreviewNumbering(1, "audio", "", "name_asc", settings)
	if err != nil {
		t.Fatal(err)
	}
	if audio.Total != 2 || audio.Items[len(audio.Items)-1].New != "8.mp3" {
		t.Fatal(audio.Items)
	}
	if err = domain.ValidateNumberingSettings(domain.NumberingSettings{Image: domain.MediaNumberingRules{Padding: 0, Lowercase: true}}); err == nil {
		t.Fatal("invalid padding accepted")
	}
	if err = domain.ValidateNumberingSettings(domain.NumberingSettings{Image: domain.MediaNumberingRules{Padding: 1, Lowercase: true, Prefix: " a "}}); err == nil {
		t.Fatal("padded prefix accepted")
	}
}
func assertOriginals(t *testing.T, root string, b Numbering) {
	t.Helper()
	for _, i := range b.Items {
		raw, err := os.ReadFile(filepath.Join(root, i.Old))
		if err != nil || string(raw) != i.Old {
			t.Fatalf("%s: %q %v", i.Old, raw, err)
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".video-rename-") {
			t.Fatalf("left temp: %s", e.Name())
		}
	}
}
func TestNumberingApplyRestoreAndIdempotence(t *testing.T) {
	l, root, b := numberedFixture(t)
	if b.Total != 5 || b.Changed != 5 {
		t.Fatal(b)
	}
	result, err := l.ApplyNumbering(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "committed" {
		t.Fatal(result)
	}
	for _, i := range b.Items {
		raw, err := os.ReadFile(filepath.Join(root, i.New))
		if err != nil || string(raw) != i.Old {
			t.Fatalf("%s: %q %v", i.New, raw, err)
		}
		e, err := l.Get(i.Entry)
		if err != nil || e.Name != i.New {
			t.Fatal(e, err)
		}
		h, err := l.History(i.Entry, 0)
		if err != nil || len(h) != 1 || h[0].Old != i.Old || h[0].New != i.New {
			t.Fatal(h, err)
		}
	}
	if _, err = l.ApplyNumbering(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "video.MP4")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "folder", "nested.JPG")); err != nil {
		t.Fatal(err)
	}
	// The saved journal survives a service restart and remains restorable.
	restarted, err := New(l.DB, root)
	if err != nil {
		t.Fatal(err)
	}
	undo, err := restarted.PreviewNumberingRestore(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ApplyNumbering(undo.ID); err != nil {
		t.Fatal(err)
	}
	assertOriginals(t, root, b)
	original, err := l.Numbering(b.ID)
	if err != nil || original.Status != "restored" {
		t.Fatal(original, err)
	}
	if _, err = l.ApplyNumbering(undo.ID); err != nil {
		t.Fatal(err)
	}
}
func TestNumberingCrossPageOrderAndSearch(t *testing.T) {
	l, root, _ := fixture(t)
	settings := domain.DefaultNumberingSettings()
	settings.Image.Compact = false
	settings.Audio.Compact = false
	for i := range 225 {
		put(t, filepath.Join(root, fmt.Sprintf("photo-%03d.PNG", i)))
	}
	put(t, filepath.Join(root, "8.jpg"))
	refresh(t, l, 1)
	b, err := l.PreviewNumbering(1, "image", "photo", "name_desc", settings)
	if err != nil {
		t.Fatal(err)
	}
	if b.Total != 225 || b.Items[0].Old != "photo-224.PNG" || b.Items[0].New != "9.png" || b.Items[224].New != "233.png" {
		t.Fatal(b.Total, b.Items[0], b.Items[224])
	}
	for _, sort := range []string{"name_asc", "name_desc", "size_asc", "size_desc", "mtime_asc", "mtime_desc", "renamed_asc", "renamed_desc"} {
		b, err = l.PreviewNumbering(1, "image", "photo", sort, settings)
		if err != nil {
			t.Fatal(err)
		}
		p, err := l.ListOrdered(t.Context(), 1, 0, 200, 0, "photo", "image", false, sort)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range p.Items {
			if b.Items[i].Entry != e.ID {
				t.Fatalf("order mismatch %s at %d", sort, i)
			}
		}
	}
}
func TestNumberingPreflightProtection(t *testing.T) {
	t.Run("changed directory", func(t *testing.T) {
		l, root, b := numberedFixture(t)
		put(t, filepath.Join(root, "new.jpg"))
		if _, err := l.ApplyNumbering(b.ID); err == nil {
			t.Fatal("stale preview accepted")
		}
		assertOriginals(t, root, b)
	})
	t.Run("unselected target", func(t *testing.T) {
		l, root, _ := fixture(t)
		put(t, filepath.Join(root, "a.jpg"))
		if err := os.Mkdir(filepath.Join(root, "1.jpg"), 0700); err != nil {
			t.Fatal(err)
		}
		refresh(t, l, 1)
		if _, err := l.PreviewNumbering(1, "image", "", "name_asc", domain.DefaultNumberingSettings()); err == nil {
			t.Fatal("directory conflict accepted")
		}
	})
	t.Run("busy", func(t *testing.T) {
		l, root, b := numberedFixture(t)
		l.Busy = func(int64) bool { return true }
		if _, err := l.ApplyNumbering(b.ID); err == nil {
			t.Fatal("busy accepted")
		}
		assertOriginals(t, root, b)
	})
	t.Run("later rename prevents restore", func(t *testing.T) {
		l, _, b := numberedFixture(t)
		if _, err := l.ApplyNumbering(b.ID); err != nil {
			t.Fatal(err)
		}
		e, _ := l.Get(b.Items[0].Entry)
		if _, err := l.Rename(e.ID, e.Version, "later.jpg", "later"); err != nil {
			t.Fatal(err)
		}
		if _, err := l.PreviewNumberingRestore(b.ID); err == nil {
			t.Fatal("later change accepted")
		}
	})
	t.Run("hard links", func(t *testing.T) {
		l, root, _ := fixture(t)
		put(t, filepath.Join(root, "a.jpg"))
		if err := os.Link(filepath.Join(root, "a.jpg"), filepath.Join(root, "b.png")); err != nil {
			t.Fatal(err)
		}
		refresh(t, l, 1)
		if _, err := l.PreviewNumbering(1, "image", "", "name_asc", domain.DefaultNumberingSettings()); err == nil {
			t.Fatal("hard links accepted")
		}
	})
}
func TestNumberingDatabaseFailureRollsBack(t *testing.T) {
	l, root, b := numberedFixture(t)
	if _, err := l.DB.Exec(`CREATE TRIGGER fail_numbering_history BEFORE INSERT ON operations BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ApplyNumbering(b.ID); err == nil || !strings.Contains(err.Error(), "已恢复原名") {
		t.Fatal(err)
	}
	assertOriginals(t, root, b)
	for _, i := range b.Items {
		e, err := l.Get(i.Entry)
		if err != nil || e.Name != i.Old {
			t.Fatal(e, err)
		}
	}
	var count int
	l.DB.QueryRow("SELECT count(*) FROM operations").Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
}
func TestNumberingCrashRecovery(t *testing.T) {
	for _, stage := range []string{"intent-only", "move-not-recorded", "staged", "partial-final", "rollback-partial"} {
		t.Run(stage, func(t *testing.T) {
			l, root, b := numberedFixture(t)
			if _, err := l.DB.Exec("UPDATE image_numberings SET status='running' WHERE id=?", b.ID); err != nil {
				t.Fatal(err)
			}
			if stage == "intent-only" || stage == "move-not-recorded" {
				i := b.Items[0]
				if _, err := l.DB.Exec("UPDATE image_numbering_items SET pending_name=? WHERE batch=? AND entry=?", i.Temp, b.ID, i.Entry); err != nil {
					t.Fatal(err)
				}
				if stage == "move-not-recorded" {
					if err := rename.NoReplace(filepath.Join(root, i.Old), filepath.Join(root, i.Temp)); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				for idx := range b.Items {
					if err := l.moveNumbering(&b, &b.Items[idx], root, b.Items[idx].Temp); err != nil {
						t.Fatal(err)
					}
				}
				if stage != "staged" {
					if err := l.moveNumbering(&b, &b.Items[0], root, b.Items[0].New); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "rollback-partial" {
					if err := l.moveNumbering(&b, &b.Items[0], root, b.Items[0].Temp); err != nil {
						t.Fatal(err)
					}
					if err := l.moveNumbering(&b, &b.Items[0], root, b.Items[0].Old); err != nil {
						t.Fatal(err)
					}
					l.DB.Exec("UPDATE image_numberings SET status='rollback' WHERE id=?", b.ID)
				}
			}
			restarted, err := New(l.DB, root)
			if err != nil {
				t.Fatal(err)
			}
			assertOriginals(t, root, b)
			recovered, err := restarted.Numbering(b.ID)
			if err != nil || recovered.Status != "rolled_back" {
				t.Fatal(recovered.Status, err)
			}
		})
	}
}
func TestNumberingRecoveryNeverOverwritesExternalFile(t *testing.T) {
	l, root, b := numberedFixture(t)
	l.DB.Exec("UPDATE image_numberings SET status='running' WHERE id=?", b.ID)
	i := &b.Items[0]
	if err := l.moveNumbering(&b, i, root, i.Temp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, i.Old), []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(l.DB, root)
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ := restarted.Numbering(b.ID)
	if recovered.Status != "review" {
		t.Fatal(recovered)
	}
	raw, _ := os.ReadFile(filepath.Join(root, i.Old))
	if string(raw) != "external" {
		t.Fatal("external overwritten")
	}
	if err = restarted.CheckAvailable(1); err == nil {
		t.Fatal("uncertain directory available")
	}
	if err = restarted.Refresh(t.Context(), 1); err == nil {
		t.Fatal("scanned temporary files")
	}
	if err = os.Remove(filepath.Join(root, i.Old)); err != nil {
		t.Fatal(err)
	}
	recovered, err = restarted.RecoverNumbering(b.ID)
	if err != nil || recovered.Status != "rolled_back" {
		t.Fatal(recovered, err)
	}
	assertOriginals(t, root, b)
}

func TestNumberingTargetMovesWithDifferentCaseAliasIdentity(t *testing.T) {
	snapshot := domain.Snapshot{Dev: 52, Ino: 100, Size: 4361526, ModTime: time.Unix(100, 0)}
	source := NumberingItem{Old: "1.JPG", New: "1.jpg", Identity: Identity(snapshot), Size: snapshot.Size, Mtime: stamp(snapshot)}
	alias := snapshot
	alias.Ino = 1593055
	cases := []struct {
		name     string
		target   string
		names    []string
		changed  map[string]NumberingItem
		snapshot domain.Snapshot
		want     bool
	}{
		{"case alias with different inode", "1.jpg", []string{"1.JPG"}, map[string]NumberingItem{"1.JPG": source}, alias, true},
		{"exact moving source", "1.JPG", []string{"1.JPG"}, map[string]NumberingItem{"1.JPG": source}, snapshot, true},
		{"replaced exact source", "1.JPG", []string{"1.JPG"}, map[string]NumberingItem{"1.JPG": source}, alias, false},
		{"independent lowercase file even with same identity", "1.jpg", []string{"1.JPG", "1.jpg"}, map[string]NumberingItem{"1.JPG": source}, snapshot, false},
		{"unchanged uppercase file", "1.jpg", []string{"1.JPG"}, nil, alias, false},
		{"ambiguous case aliases", "1.jpg", []string{"1.JPG", "1.JpG"}, map[string]NumberingItem{"1.JPG": source}, alias, false},
		{"unlisted target", "1.jpg", nil, map[string]NumberingItem{"1.JPG": source}, alias, false},
		{"different size", "1.jpg", []string{"1.JPG"}, map[string]NumberingItem{"1.JPG": source}, domain.Snapshot{Dev: 52, Ino: 1593055, Size: 1, ModTime: snapshot.ModTime}, false},
		{"different modification time", "1.jpg", []string{"1.JPG"}, map[string]NumberingItem{"1.JPG": source}, domain.Snapshot{Dev: 52, Ino: 1593055, Size: snapshot.Size, ModTime: snapshot.ModTime.Add(time.Second)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			names := map[string][]string{}
			for _, name := range tc.names {
				key := strings.ToLower(name)
				names[key] = append(names[key], name)
			}
			if got := numberingTargetMoves(tc.target, tc.snapshot, names, tc.changed); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestNumberingCaseOnlyApplyRestoreWithSymlinkSibling(t *testing.T) {
	l, root, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(root, "1.JPG"), []byte("original picture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing.jpg", filepath.Join(root, "linked.jpg")); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	batch, err := l.PreviewNumbering(1, "image", "", "name_asc", domain.DefaultNumberingSettings())
	if err != nil || batch.Total != 1 || batch.Items[0].New != "1.jpg" {
		t.Fatalf("%+v %v", batch, err)
	}
	if _, err := l.ApplyNumbering(batch.ID); err != nil {
		t.Fatal(err)
	}
	undo, err := l.PreviewNumberingRestore(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ApplyNumbering(undo.ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "1.JPG"))
	if err != nil || string(data) != "original picture" {
		t.Fatalf("%q %v", data, err)
	}
	if target, err := os.Readlink(filepath.Join(root, "linked.jpg")); err != nil || target != "missing.jpg" {
		t.Fatalf("symlink changed: %q %v", target, err)
	}
}
