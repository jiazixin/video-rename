package library

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func TestNumberingTreeIndexesEveryLevelAndRestores(t *testing.T) {
	l, root, _ := fixture(t)
	for _, dir := range []string{"child/deep", "sibling", "empty"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"photo.JPG": "1.jpg", "song.MP3": "1.mp3",
		"child/5.png": "child/5.png", "child/photo.JPG": "child/6.jpg", "child/song.MP3": "child/1.mp3",
		"child/deep/photo.JPG": "child/deep/1.jpg", "child/deep/song.MP3": "child/deep/1.mp3",
		"sibling/photo.JPG": "sibling/1.jpg", "clip.mp4": "clip.mp4", "notes.txt": "notes.txt",
	}
	for name := range maps.Keys(files) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	put(t, filepath.Join(outside, "untouched.JPG"))
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	// No directory has been opened or indexed before this call.
	settings := domain.DefaultNumberingSettings()
	settings.Image.Compact = false
	settings.Audio.Compact = false
	batches, err := l.PreviewNumberingTree(t.Context(), 1, settings)
	if err != nil || len(batches) != 4 {
		t.Fatalf("preview: %v, %v", batches, err)
	}
	for _, b := range batches {
		if _, err := l.ApplyNumbering(b.ID); err != nil {
			t.Fatalf("apply %s: %v", b.Path, err)
		}
	}
	for original, renamed := range files {
		data, err := os.ReadFile(filepath.Join(root, renamed))
		if err != nil || string(data) != original {
			t.Fatalf("%s -> %s: %q %v", original, renamed, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "untouched.JPG")); err != nil {
		t.Fatal(err)
	}
	history, err := l.NumberingTreeHistory(1)
	if err != nil || len(history) != 4 {
		t.Fatalf("history: %v, %v", history, err)
	}
	for _, b := range history {
		undo, err := l.PreviewNumberingRestore(b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = l.ApplyNumbering(undo.ID); err != nil {
			t.Fatal(err)
		}
	}
	for original := range maps.Keys(files) {
		if _, err := os.Stat(filepath.Join(root, original)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNumberingTreeSharedRulesAndChangedDirectory(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"pic.JPG", "song.MP3", "5.mp3"} {
		put(t, filepath.Join(root, name))
	}
	settings := domain.DefaultNumberingSettings()
	settings.Image.Compact = false
	settings.Audio.Compact = false
	settings.Shared = true
	settings.Image.Padding = 3
	settings.Audio.Padding = 2
	batches, err := l.PreviewNumberingTree(t.Context(), 1, settings)
	if err != nil || len(batches) != 1 {
		t.Fatalf("preview: %v %v", batches, err)
	}
	want := map[string]string{"pic.JPG": "006.jpg", "song.MP3": "07.mp3", "5.mp3": "05.mp3"}
	for _, item := range batches[0].Items {
		if item.New != want[item.Old] {
			t.Fatalf("unexpected numbering: %+v", item)
		}
	}
	put(t, filepath.Join(root, "new.jpg"))
	if _, err := l.ApplyNumbering(batches[0].ID); err == nil {
		t.Fatal("accepted changes after preview")
	}
	for name := range maps.Keys(want) {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := l.PreviewNumberingTree(ctx, 1, settings); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestNumberingTreeSharedCompactReservesOnlyKeptNumbers(t *testing.T) {
	for _, audioCompact := range []bool{true, false} {
		t.Run(fmt.Sprintf("audioCompact=%v", audioCompact), func(t *testing.T) {
			l, root, _ := fixture(t)
			for _, name := range []string{"x.jpg", "1.mp3"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			settings := domain.DefaultNumberingSettings()
			settings.Shared = true
			settings.Audio.Compact = audioCompact
			batches, err := l.PreviewNumberingTree(t.Context(), 1, settings)
			if err != nil || len(batches) != 1 {
				t.Fatalf("preview: %v %v", batches, err)
			}
			want := map[string]string{"x.jpg": "1.jpg", "1.mp3": "2.mp3"}
			if !audioCompact {
				want = map[string]string{"x.jpg": "2.jpg", "1.mp3": "1.mp3"}
			}
			for _, item := range batches[0].Items {
				if item.New != want[item.Old] {
					t.Fatalf("unexpected numbering: %+v", item)
				}
			}
			if _, err := l.ApplyNumbering(batches[0].ID); err != nil {
				t.Fatal(err)
			}
			for original, renamed := range want {
				data, err := os.ReadFile(filepath.Join(root, renamed))
				if err != nil || string(data) != original {
					t.Fatalf("%s: %q %v", renamed, data, err)
				}
			}
		})
	}
}

func TestNumberingTreeWithPendingRefreshFlag(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "photo.jpg"))
	// The worker may be waiting for l.mu, or finished but waiting to clear this flag.
	l.refreshMu.Lock()
	l.refreshing[1] = true
	l.refreshMu.Unlock()
	batches, err := l.PreviewNumberingTree(t.Context(), 1, domain.DefaultNumberingSettings())
	if err != nil || len(batches) != 1 || batches[0].Items[0].New != "1.jpg" {
		t.Fatalf("preview: %+v, %v", batches, err)
	}
}

func TestNumberingTreeConflictDiagnostics(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			l, root, _ := fixture(t)
			dir := filepath.Join(root, "parent", "child")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(dir, "photo.jpg"))
			target := filepath.Join(dir, "1.jpg")
			var err error
			if kind == "directory" {
				err = os.Mkdir(target, 0700)
			} else {
				err = os.Symlink("missing.jpg", target)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = l.PreviewNumberingTree(t.Context(), 1, domain.DefaultNumberingSettings())
			diagnostic, ok := errors.AsType[*domain.JobError](err)
			if !ok {
				t.Fatalf("missing diagnostic: %v", err)
			}
			for _, want := range []string{"photo.jpg", "1.jpg", "目标类型：" + kind, "未参与本次编号", "此次预览未修改文件"} {
				if !strings.Contains(diagnostic.ProcessingLog, want) {
					t.Fatalf("missing %q: %s", want, diagnostic.ProcessingLog)
				}
			}
			if !strings.Contains(err.Error(), "parent/child") {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "photo.jpg")); err != nil {
				t.Fatal(err)
			}
			history, err := l.NumberingTreeHistory(1)
			if err != nil || len(history) != 0 {
				t.Fatalf("preview created execution history: %+v %v", history, err)
			}
		})
	}
}

func TestNumberingTreeCompactNumericNamesAndCaseAliases(t *testing.T) {
	l, root, _ := fixture(t)
	files := []string{"000.jpg", "001.JPG", "2.jpg", "10.jpg", "photo.JPG"}
	for _, dir := range []string{"first", "second/deep"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if err := os.WriteFile(filepath.Join(root, dir, name), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	settings := domain.DefaultNumberingSettings()
	settings.Image.Compact = true
	batches, err := l.PreviewNumberingTree(t.Context(), 1, settings)
	if err != nil || len(batches) != 2 {
		t.Fatalf("preview: %+v %v", batches, err)
	}
	for _, batch := range batches {
		for i, item := range batch.Items {
			if item.Old != files[i] || item.New != fmt.Sprintf("%d.jpg", i+1) {
				t.Fatalf("unexpected item: %+v", item)
			}
		}
		if _, err := l.ApplyNumbering(batch.ID); err != nil {
			t.Fatal(err)
		}
		for _, item := range batch.Items {
			data, err := os.ReadFile(filepath.Join(root, batch.Path, item.New))
			if err != nil || string(data) != item.Old {
				t.Fatalf("lost content: %+v %q %v", item, data, err)
			}
		}
	}
	for _, batch := range batches {
		undo, err := l.PreviewNumberingRestore(batch.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.ApplyNumbering(undo.ID); err != nil {
			t.Fatal(err)
		}
	}
}
