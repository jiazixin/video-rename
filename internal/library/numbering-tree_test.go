package library

import (
	"context"
	"maps"
	"os"
	"path/filepath"
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
	batches, err := l.PreviewNumberingTree(t.Context(), 1, domain.DefaultNumberingSettings())
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
