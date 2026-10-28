package job

import (
	"context"
	"database/sql"
	"sync"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

// Dispatch only when an extraction slot is available. Release that slot once
// frames are cached; waiting for inference is bounded by pinned cache capacity,
// not by a fixed number of videos in the run.
func (m *Manager) processPending(ctx context.Context, run Run, cfg config.TaskConfig) (result error) {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var firstFailure sync.Once
	var workerErr error
	defer func() {
		if result != nil {
			cancel()
		}
		wg.Wait()
		cancel()
		if workerErr != nil {
			result = workerErr
		}
	}()
	var after int64
	for ctx.Err() == nil {
		var batch []item
		err := m.db.View(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, "SELECT id,entry,identity,size,mtime_ns FROM run_items WHERE run=? AND status='queued' AND id>? ORDER BY id LIMIT 128", run.ID, after)
			if err != nil {
				return err
			}
			batch, err = storage.CollectRows(rows, func(rows *sql.Rows) (item, error) {
				var it item
				err := rows.Scan(&it.ID, &it.Entry, &it.Identity, &it.Size, &it.ModifiedNS)
				return it, err
			})
			return err
		})
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, it := range batch {
			release, err := m.extractionSlot(ctx, run.ID)
			if err != nil {
				return err
			}
			// Early validation failures and cancellation must also release the slot.
			extracted := sync.OnceFunc(release)
			if ctx.Err() != nil {
				extracted()
				return nil
			}
			wg.Go(func() {
				defer extracted()
				copy := cfg
				if err := m.process(ctx, processOptions{Batch: run, It: it, Config: &copy, Extracted: extracted}); err != nil {
					firstFailure.Do(func() {
						workerErr = err
						cancel()
						// Stage-slot waits use the active run's scheduling context.
						// Cancel it too, including workers waiting behind another batch.
						m.mu.Lock()
						if active, ok := m.active[run.ID]; ok {
							active.cancel()
						}
						m.mu.Unlock()
					})
				}
			})
			after = it.ID
		}
	}
	return nil
}
