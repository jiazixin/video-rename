package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func TestNumberingSkipsMediaMarksAndReservesNumbers(t *testing.T) {
	for _, media := range []string{"image", "audio", "mixed"} {
		for _, shared := range []bool{false, true} {
			for _, compact := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/shared=%v/compact=%v", media, shared, compact), func(t *testing.T) {
					l, root, _ := fixture(t)
					for _, name := range []string{"pic-001.JPG", "aud-002.MP3", "my-picture.png", "my-song.flac", "photo.JPG", "song.MP3"} {
						put(t, filepath.Join(root, name))
					}
					refresh(t, l, 1)
					ids := []int64{}
					for _, name := range []string{"pic-001.JPG", "aud-002.MP3", "my-picture.png", "my-song.flac"} {
						ids = append(ids, entry(t, l, 1, name).ID)
					}
					if r := l.SetProcessed(t.Context(), ids, true); r.Changed != 4 || r.Failed != 0 {
						t.Fatal(r)
					}
					settings := domain.DefaultNumberingSettings()
					settings.Shared = shared
					settings.Image.Prefix, settings.Audio.Prefix = "pic-", "aud-"
					settings.Image.Padding, settings.Audio.Padding = 3, 3
					settings.Image.Compact, settings.Audio.Compact = compact, compact
					b, err := l.PreviewNumbering(1, media, "", "name_asc", settings)
					if err != nil {
						t.Fatal(err)
					}
					want := map[string]string{"photo.JPG": "pic-002.jpg", "song.MP3": "aud-003.mp3"}
					if compact {
						want["song.MP3"] = "aud-001.mp3"
					}
					if shared {
						want["photo.JPG"] = "pic-003.jpg"
						want["song.MP3"] = "aud-003.mp3"
						if media == "mixed" {
							want["song.MP3"] = "aud-004.mp3"
						}
					}
					total := 1
					if media == "mixed" {
						total = 2
					}
					if b.Total != total {
						t.Fatalf("protected files joined preview: %+v", b)
					}
					for _, item := range b.Items {
						if item.New != want[item.Old] {
							t.Fatalf("wrong reserved number: %+v; want %s", item, want[item.Old])
						}
					}
					if _, err := l.ApplyNumbering(b.ID); err != nil {
						t.Fatal(err)
					}
					for _, id := range ids {
						e, err := l.Get(id)
						if err != nil || e.Processed != "marked" {
							t.Fatalf("lost protection: %+v %v", e, err)
						}
						if _, err := os.Stat(filepath.Join(root, e.Name)); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func TestNumberingRejectsMarkAddedAfterPreview(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"photo.JPG", "song.MP3"} {
		put(t, filepath.Join(root, name))
	}
	refresh(t, l, 1)
	settings := domain.DefaultNumberingSettings()
	b, err := l.PreviewNumbering(1, "mixed", "", "name_asc", settings)
	if err != nil {
		t.Fatal(err)
	}
	e := entry(t, l, 1, "song.MP3")
	if r := l.SetProcessed(t.Context(), []int64{e.ID}, true); r.Changed != 1 || r.Failed != 0 {
		t.Fatal(r)
	}
	if _, err := l.ApplyNumbering(b.ID); err == nil || !strings.Contains(err.Error(), "跳过编号") {
		t.Fatalf("old preview ignored new protection: %v", err)
	}
	for _, name := range []string{"photo.JPG", "song.MP3"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("old preview moved file: %v", err)
		}
	}
	if r := l.SetProcessed(t.Context(), []int64{e.ID}, false); r.Changed != 1 || r.Failed != 0 {
		t.Fatal(r)
	}
	if _, err := l.ApplyNumbering(b.ID); err != nil {
		t.Fatalf("unmarked file cannot be numbered: %v", err)
	}
}

func TestNumberingTreeTraversesProtectedDirectories(t *testing.T) {
	l, root, _ := fixture(t)
	if err := os.MkdirAll(filepath.Join(root, "child", "deep"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "photo.JPG"))
	put(t, filepath.Join(root, "child", "song.MP3"))
	if r := l.SetProcessed(t.Context(), []int64{1}, true); r.Changed != 2 || r.Failed != 0 {
		t.Fatal(r)
	}
	put(t, filepath.Join(root, "child", "deep", "new.JPG"))
	batches, err := l.PreviewNumberingTree(t.Context(), 1, domain.DefaultNumberingSettings())
	if err != nil || len(batches) != 1 || batches[0].Path != filepath.Join("child", "deep") || batches[0].Total != 1 {
		t.Fatalf("protected ancestor blocked new descendant: %+v %v", batches, err)
	}
	if _, err := l.ApplyNumbering(batches[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"photo.JPG", "child/song.MP3", "child/deep/1.jpg"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
}
