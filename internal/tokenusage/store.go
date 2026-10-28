package tokenusage

import (
	"context"
	"database/sql"
	"time"

	"video-rename/internal/storage"
)

// RecordAttemptOptions groups the inputs for RecordAttempt.
type RecordAttemptOptions struct {
	Store     *storage.Store
	Run       int64
	Item      int64
	Attempt   int
	ProfileID int64
	Title     string
	Summary   string
	Request   string
	Response  string
	At        time.Time
}

// RecordAttempt counts one model exchange and appends it to the per-request ledger,
// advancing the run aggregate in the same short transaction. The unique item/attempt
// makes the write idempotent: a repeated record for the same run item and attempt is
// ignored, and the aggregate only moves when a ledger row is actually inserted.
// Exchanges without a countable usage stay unwritten, matching the aggregate's
// historical semantics.
func RecordAttempt(ctx context.Context, params RecordAttemptOptions) error {
	db, run, item, attempt, profileID, title, summary, request, response, at := params.Store, params.Run, params.Item, params.Attempt, params.ProfileID, params.Title, params.Summary, params.Request, params.Response, params.At

	tokens, estimated, ok := Count(request, response)
	if !ok {
		return nil
	}
	rough := 0
	if estimated {
		rough = 1
	}
	return db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		inserted, err := tx.ExecContext(ctx, `INSERT INTO token_usage_records(run,item,attempt,model_config_id,model_config_title,config_summary,recorded_at,tokens,records,estimated_records) VALUES(?,?,?,?,?,?,?,?,1,?) ON CONFLICT(item,attempt) DO NOTHING`, run, item, attempt, profileID, title, summary, at.UnixMilli(), tokens, rough)
		if err != nil {
			return err
		}
		count, err := inserted.RowsAffected()
		if err != nil {
			return err
		}
		if count > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO run_token_usage(run,token_count,token_records,token_estimated_records) VALUES(?,?,1,?)
ON CONFLICT(run) DO UPDATE SET token_count=token_count+excluded.token_count,token_records=token_records+1,token_estimated_records=token_estimated_records+excluded.token_estimated_records`, run, tokens, rough); err != nil {
				return err
			}
		}
		return nil
	})
}
