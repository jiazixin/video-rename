package tokenusage

import (
	"database/sql/driver"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("video_rename_model_key", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		summary, _ := args[0].(string)
		return parseMeta("", 0, summary, 0, "").key, nil
	})
}
