package jobs

import (
	"context"
	"sync"
	"video-rename/internal/config"
)

// Each run keeps a bounded lookahead. Actual extraction and inference slots are
// global and independent; a blocked model does not occupy an extraction slot.
func (m *Manager) processPending(ctx context.Context, run Run, cfg config.Config) error {
	slots := make(chan struct{}, 64)
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
			select {
			case <-ctx.Done():
				return nil
			case slots <- struct{}{}:
			}
			if ctx.Err() != nil {
				<-slots
				return nil
			}
			wg.Add(1)
			go func(it item) {
				defer wg.Done()
				defer func() { <-slots }()
				copy := cfg
				m.process(ctx, run, it, &copy)
			}(it)
			after = it.ID
		}
	}
	return nil
}
