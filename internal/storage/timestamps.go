package storage

import (
	"database/sql/driver"
	"time"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("video_rename_start_ms", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		for _, arg := range args {
			value, _ := arg.(string)
			if at, err := time.Parse(time.RFC3339, value); err == nil && at.UnixMilli() != 0 {
				return at.UnixMilli(), nil
			}
		}
		return int64(0), nil
	})
}
