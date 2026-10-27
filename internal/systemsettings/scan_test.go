package systemsettings

import (
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"video-rename/internal/storage"
)

func TestScanSettingsDefaultPersistenceAndIndependentUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.sqlite")
	db, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db}
	got, err := store.Scan(t.Context())
	if err != nil || !reflect.DeepEqual(got, DefaultScanSettings()) || got.IntervalMinutes != 60 || !got.Enabled {
		t.Fatalf("default scan: %+v %v", got, err)
	}
	scan := ScanSettings{Enabled: false, IntervalMinutes: 120, Targets: []int64{2, 3}, ExcludedTargets: []int64{4, 5}}
	if err := store.SaveScan(t.Context(), scan); err != nil {
		t.Fatal(err)
	}
	processing := Default()
	processing.ExtractWorkers = 3
	processing.RetryCount = 5
	processing.RunningBatchLimit, processing.WaitingBatchLimit = 64, 5000
	if err := store.Save(t.Context(), processing); err != nil {
		t.Fatal(err)
	}
	got, err = store.Scan(t.Context())
	if err != nil || !reflect.DeepEqual(got, scan) {
		t.Fatalf("processing save overwrote scan: %+v %v", got, err)
	}
	scan.Enabled = true
	if err := store.SaveScan(t.Context(), scan); err != nil {
		t.Fatal(err)
	}
	actual, err := store.Get(t.Context())
	if err != nil || actual != processing {
		t.Fatalf("scan save overwrote processing: %+v %v", actual, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err = (Store{DB: db}).Scan(t.Context())
	if err != nil || !reflect.DeepEqual(got, scan) {
		t.Fatalf("scan settings not retained: %+v %v", got, err)
	}
}

func TestScanSettingsValidationAndStorageErrors(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "scan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db}
	for _, v := range []ScanSettings{
		{IntervalMinutes: 0, Targets: []int64{1}},
		{IntervalMinutes: 4, Targets: []int64{1}},
		{IntervalMinutes: 1441, Targets: []int64{1}},
		{IntervalMinutes: 60},
		{IntervalMinutes: 60, Targets: []int64{0}},
		{IntervalMinutes: 60, Targets: []int64{1, 1}},
		{IntervalMinutes: 60, Targets: []int64{1}, ExcludedTargets: []int64{0}},
		{IntervalMinutes: 60, Targets: []int64{1}, ExcludedTargets: []int64{2, 2}},
		{IntervalMinutes: 60, Targets: []int64{1}, ExcludedTargets: make([]int64, 101)},
	} {
		if err := store.SaveScan(t.Context(), v); err == nil {
			t.Fatalf("invalid settings accepted: %+v", v)
		}
	}
	if err := store.Save(t.Context(), Default()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE system_settings SET policy='{"library_scan":{"enabled":true,"interval_minutes":0,"targets":[1]}}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(t.Context()); err == nil {
		t.Fatal("invalid stored scan settings returned defaults")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(t.Context()); err == nil {
		t.Fatal("closed database returned defaults")
	}
}

func TestConcurrentScanAndProcessingSettingsSaves(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "scan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := Store{DB: db}
	scan := ScanSettings{Enabled: true, IntervalMinutes: 90, Targets: []int64{1, 2}, ExcludedTargets: []int64{}}
	processing := Default()
	processing.CacheGB = 2
	processing.RetryCount = 6
	processing.RunningBatchLimit, processing.WaitingBatchLimit = 48, 3000
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if err := store.SaveScan(t.Context(), scan); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := store.Save(t.Context(), processing); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	gotScan, err := store.Scan(t.Context())
	if err != nil || !reflect.DeepEqual(gotScan, scan) {
		t.Fatalf("lost scan save: %+v %v", gotScan, err)
	}
	gotProcessing, err := store.Get(t.Context())
	if err != nil || gotProcessing != processing {
		t.Fatalf("lost processing save: %+v %v", gotProcessing, err)
	}
}
