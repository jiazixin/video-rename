package domain

// ActiveRunStatusesSQL lists the run states that still own their targets. SQL
// embeds it verbatim so queries keep matching the run_active partial index in
// internal/storage/schema.sql.
const ActiveRunStatusesSQL = "('queued','scanning','running','paused','cancelling')"

// ActiveRunStatus reports whether a run in this state still owns its targets.
func ActiveRunStatus(status string) bool {
	switch status {
	case "queued", "scanning", "running", "paused", "cancelling":
		return true
	}
	return false
}
