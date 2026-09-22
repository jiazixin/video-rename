package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/models"
)

// processPending dispatches only when the model's latest concurrency permits it.
// Each item gets its own config copy; in-flight requests keep their original settings.
func (m *Manager) processPending(ctx context.Context, run Run, cfg config.Config) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	finished := make(chan struct{}, 32)
	active, limit := 0, cfg.Workers
	defer func() {
		for active > 0 {
			<-finished
			active--
		}
	}()
	var after int64
	for ctx.Err() == nil {
		rows, err := m.DB.QueryContext(ctx, "SELECT id,entry,identity,size,mtime FROM run_items WHERE run=? AND status='queued' AND id>? ORDER BY id LIMIT 128", run.ID, after)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		batch := []item{}
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
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, it := range batch {
			for {
				if ctx.Err() != nil {
					return nil
				}
				latest, err := (models.Store{DB: m.DB}).Workers(ctx, run.ModelConfigID)
				if err == nil {
					limit = latest
				} else if !errors.Is(err, sql.ErrNoRows) {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				// A deleted profile keeps the last known limit (or the creation snapshot).
				if active < limit {
					break
				}
				select {
				case <-ctx.Done():
					return nil
				case <-finished:
					active--
				case <-ticker.C:
				}
			}
			if ctx.Err() != nil {
				return nil
			}
			itemConfig := cfg
			itemConfig.Workers, itemConfig.Server.Workers = limit, limit
			active++
			go func(it item, itemConfig config.Config) {
				defer func() { finished <- struct{}{} }()
				if ctx.Err() == nil {
					m.process(ctx, run.ID, it, &itemConfig)
				}
			}(it, itemConfig)
			after = it.ID
		}
	}
	return nil
}
