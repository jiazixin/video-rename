package sampling

import (
	"path/filepath"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func TestPersistenceAndRejectedSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db}
	value, err := store.Get(t.Context())
	if err != nil || value.Sampling.ParsedStrategy != config.StrategyDuration {
		t.Fatal(value, err)
	}
	value.Sampling.MaxFrames = 32
	value.Images.MaxDimension = 1024
	if err = store.Save(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	bad := value
	bad.Images.JPEGQuality = 99
	if err = store.Save(t.Context(), bad); err == nil {
		t.Fatal("invalid save accepted")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := (Store{DB: db}).Get(t.Context())
	if err != nil || got.Sampling.MaxFrames != 32 || got.Images.MaxDimension != 1024 || got.Images.JPEGQuality != 4 {
		t.Fatal(got, err)
	}
	if len(got.Sampling.ParsedDuration) != 5 {
		t.Fatal("parsed rules missing")
	}
}
