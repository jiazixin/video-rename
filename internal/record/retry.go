package record

import (
	"context"
	"database/sql"
	"errors"

	"video-rename/internal/domain"
)

// RetryRecord identifies a prior automatic operation.
type RetryRecord struct{ ID, Entry, Operation int64 }

// RetryProblem describes a record that cannot be retried.
type RetryProblem struct {
	Record int64         `json:"record,omitzero"`
	Files  int           `json:"files,omitzero"`
	Error  domain.Record `json:"error"`
}

// AllRetryableFailures lists every automatic failure with a file; other
// failures are counted as skipped.
func (s *Store) AllRetryableFailures(ctx context.Context) (records []RetryRecord, skipped int, err error) {
	err = s.db.View(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id,entry,action FROM events WHERE category='failure' ORDER BY id DESC")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record RetryRecord
			var action string
			if err = rows.Scan(&record.ID, &record.Entry, &action); err != nil {
				return err
			}
			if action != "automatic" || record.Entry <= 0 {
				skipped++
				continue
			}
			records = append(records, record)
		}
		return rows.Err()
	})
	return
}

// ChosenRetryRecords loads the chosen automatic records of one category and
// reports the ones that cannot be retried.
func (s *Store) ChosenRetryRecords(ctx context.Context, ids []int64, category string) (records []RetryRecord, problems []RetryProblem, err error) {
	for _, id := range ids {
		record := RetryRecord{ID: id}
		err := s.db.View(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT entry,operation FROM events WHERE id=? AND category=? AND action='automatic' AND entry>0", id, category).Scan(&record.Entry, &record.Operation)
		})
		if errors.Is(err, sql.ErrNoRows) {
			problems = append(problems, RetryProblem{Record: id, Error: domain.Record{Code: "record_not_retryable", Message: "record does not exist or cannot be retried automatically"}})
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		records = append(records, record)
	}
	return records, problems, nil
}
