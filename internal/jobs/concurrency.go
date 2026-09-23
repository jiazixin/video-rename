package jobs

import (
	"context"
	"sync"
	"video-rename/internal/config"
	"video-rename/internal/systemsettings"
)

// Dispatch only when an extraction slot is available. Release that slot once
// frames are cached; waiting for inference is bounded by pinned cache capacity,
// not by a fixed number of videos in the run.
func (m *Manager) processPending(ctx context.Context, run Run, cfg config.Config) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	var after int64
	for ctx.Err() == nil {
		rows, err := m.DB.QueryContext(ctx, "SELECT id,entry,identity,size,mtime FROM run_items WHERE run=? AND status='queued' AND id>? ORDER BY id LIMIT 128", run.ID, after)
		if err != nil {
			return err
		}
		var batch []item
		for rows.Next() {
			var it item
			if err = rows.Scan(&it.ID, &it.Entry, &it.Identity, &it.Size, &it.Mtime); err != nil {
				break
			}
			batch = append(batch, it)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, it := range batch {
			release, _, err := m.extractSlots.acquire(ctx, 0, 8, func() (int, error) {
				settings, err := (systemsettings.Store{DB: m.DB}).Get()
				return settings.ExtractWorkers, err
			})
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
				m.process(ctx, run, it, &copy, extracted)
			})
			after = it.ID
		}
	}
	return nil
}
