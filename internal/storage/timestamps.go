package storage

import (
	"database/sql/driver"
	"fmt"
	"time"

	"modernc.org/sqlite"

	"video-rename/internal/timefmt"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("video_rename_start_ms", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		for index, arg := range args {
			field := []string{"started", "created"}[index]
			if arg == nil {
				continue
			}
			value, ok := arg.(string)
			if !ok {
				return nil, fmt.Errorf("stored %s timestamp is not text", field)
			}
			if value == "" {
				continue
			}
			var at timefmt.Time
			if err := at.UnmarshalText([]byte(value)); err != nil {
				return nil, fmt.Errorf("parse stored %s timestamp: %w", field, err)
			}
			return time.Time(at).UnixMilli(), nil
		}
		return int64(0), nil
	})
}
