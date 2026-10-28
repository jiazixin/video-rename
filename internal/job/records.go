package job

import "video-rename/internal/domain"

// Stored outcome records of run items, runs and extraction records. Clients map
// the codes to text; see docs/API.md.
var (
	recordSkipProcessed       = domain.Record{Code: "skip_processed", Message: "already processed; skipped"}.Text()
	recordSkipUnstable        = domain.Record{Code: "skip_unstable", Message: "file is not stable yet; left for the next run"}.Text()
	recordCancelled           = domain.Record{Code: "cancelled", Message: "cancelled"}.Text()
	recordItemInterrupted     = domain.Record{Code: "interrupted_by_restart", Message: "service restarted; processing interrupted"}.Text()
	recordRunInterrupted      = domain.Record{Code: "run_interrupted_by_restart", Message: "service restarted; the previous run was interrupted"}.Text()
	recordExtractInterrupted  = domain.Record{Code: "extraction_interrupted_by_restart", Message: "service restarted; extraction interrupted"}.Text()
	recordExtractionCancelled = domain.Record{Code: "extraction_cancelled", Message: "extraction cancelled"}.Text()
)

// errUnstable is the extraction failure of a file skipped as unstable.
var errUnstable = domain.Fail(domain.KindConflict, domain.Record{Code: "skip_unstable", Message: "file is not stable yet; left for the next run"})

// runFailedRecord is the stored message of items cancelled because their run failed.
func runFailedRecord(cause error) string {
	return domain.Record{Code: "run_failed", Message: "run failed; processing stopped: " + cause.Error(), Details: map[string]any{"cause": domain.RecordOf(cause)}}.Text()
}
