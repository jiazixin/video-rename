package tokenusage

import (
	"database/sql/driver"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("video_rename_model_key", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		summary, _ := args[0].(string)
		return parseMeta(parseMetaOptions{Source: "", Plan: 0, Summary: summary, ProfileID: 0, ProfileTitle: ""}).key, nil
	})
}
