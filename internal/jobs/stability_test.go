package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/systemsettings"
)

func TestStabilityZeroDisablesWait(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Analyze = func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error) {
		return "renamed", nil
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "zero-stability"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	m.Execute(ctx, id)
	run, err := m.Get(t.Context(), id)
	if err != nil || run.Done != 1 || run.Status != "completed" {
		t.Fatal(run, err)
	}
}

func TestStabilityWaitProtectsChangedFileAndCanBeCancelled(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "source-change", true: "cancel-wait"}[cancelled], func(t *testing.T) {
			m, root := setup(t)
			path := filepath.Join(root, "a.mp4")
			if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
				t.Fatal(err)
			}
			settings := systemsettings.Default()
			settings.StabilitySeconds = 1
			store := systemsettings.Store{DB: m.DB}
			if err := store.Save(settings); err != nil {
				t.Fatal(err)
			}
			calls := make(chan struct{}, 1)
			m.Analyze = func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error) {
				calls <- struct{}{}
				return "renamed", nil
			}
			id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "wait-stability"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan struct{})
			started := time.Now()
			go func() { defer close(done); m.Execute(ctx, id) }()
			defer func() { cancel(); <-done }()
			for {
				var count int
				if err = m.DB.QueryRow("SELECT count(*) FROM run_items WHERE run=?", id).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count > 0 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("scan timed out")
				case <-time.After(5 * time.Millisecond):
				}
			}
			if cancelled {
				if err = m.Cancel(id); err != nil {
					t.Fatal(err)
				}
			} else {
				// Updating the setting must not shorten this run's captured wait.
				settings.StabilitySeconds = 0
				if err = store.Save(settings); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, []byte("still writing more video"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("run did not finish")
			}
			run, err := m.Get(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-calls:
				t.Fatal("unstable or cancelled video reached model")
			default:
			}
			if cancelled {
				if run.Status != "cancelled" {
					t.Fatal(run)
				}
			} else {
				if run.Skipped != 1 || run.Status != "completed" {
					t.Fatal(run)
				}
				if time.Since(started) < time.Second {
					t.Fatal("existing wait was shortened")
				}
			}
		})
	}
}
