package storage

import (
	"database/sql"
	"errors"
	"fmt"
)

// CollectRows consumes and closes rows, including on scan or iteration failure.
// Successful empty results are non-nil; failures return no partial values.
func CollectRows[T any](rows *sql.Rows, scan func(*sql.Rows) (T, error)) (items []T, err error) {
	items = make([]T, 0)
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database rows: %w", closeErr))
		}
		if err != nil {
			items = nil
		}
	}()
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan database row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read database rows: %w", err)
	}
	return items, nil
}
