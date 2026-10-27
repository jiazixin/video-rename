package tokenusage

import (
	"context"
	"time"

	"video-rename/internal/storage"
)

// RecordAttempt counts one model exchange and appends it to the per-request ledger,
// advancing the run aggregate in the same short transaction. The unique item/attempt
// makes the write idempotent: a repeated record for the same run item and attempt is
// ignored, and the aggregate only moves when a ledger row is actually inserted.
// Exchanges without a countable usage stay unwritten, matching the aggregate's
// historical semantics.
func RecordAttempt(ctx context.Context, db *storage.Store, run, item int64, attempt int, profileID int64, title, summary, request, response string, at time.Time) error {
	tokens, estimated, ok := Count(request, response)
	if !ok {
		return nil
	}
	rough := 0
	if estimated {
		rough = 1
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	inserted, err := tx.ExecContext(ctx, `INSERT INTO token_usage_records(run,item,attempt,model_config_id,model_config_title,config_summary,recorded_at,tokens,records,estimated_records) VALUES(?,?,?,?,?,?,?,?,1,?) ON CONFLICT(item,attempt) DO NOTHING`, run, item, attempt, profileID, title, summary, at.UnixMilli(), tokens, rough)
	if err != nil {
		return err
	}
	if count, _ := inserted.RowsAffected(); count > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO run_token_usage(run,token_count,token_records,token_estimated_records) VALUES(?,?,1,?)
ON CONFLICT(run) DO UPDATE SET token_count=token_count+excluded.token_count,token_records=token_records+1,token_estimated_records=token_estimated_records+excluded.token_estimated_records`, run, tokens, rough); err != nil {
			return err
		}
	}
	return tx.Commit()
}
