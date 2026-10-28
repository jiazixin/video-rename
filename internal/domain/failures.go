package domain

// FailureReason identifies an expected domain failure independently of any transport.
type FailureReason int

// Domain failure reasons.
const (
	ReasonAlreadyProcessed FailureReason = iota + 1
	ReasonCacheClearIncomplete
	ReasonDeleteIncomplete
	ReasonDeleteIndexFailed
	ReasonDeleteListingFailed
	ReasonDescendantBusy
	ReasonDirectoryChanged
	ReasonDiscoveryCursorInvalid
	ReasonDiscoveryHttpError
	ReasonDiscoveryInvalid
	ReasonDiscoveryReadFailed
	ReasonDiscoveryRequestInvalid
	ReasonDiscoveryTimeout
	ReasonDiscoveryTooLarge
	ReasonDiscoveryUnreachable
	ReasonEntryBusy
	ReasonEntryChanged
	ReasonEntryNotIndexed
	ReasonFallbackModelInvalid
	ReasonFallbackModelUnavailable
	ReasonFramesOverReservation
	ReasonIdempotencyCapacity
	ReasonIdempotencyKeyReused
	ReasonIdempotencyPending
	ReasonIdentityChanged
	ReasonIllegalPath
	ReasonInvalidCredentials
	ReasonInvalidHierarchy
	ReasonJournalSourceChanged
	ReasonLibraryHalted
	ReasonMarkBatchFailed
	ReasonMarkResultGone
	ReasonMarkRunning
	ReasonModelConfigChanged
	ReasonModelConfigInUse
	ReasonModelConfigIncomplete
	ReasonModelConfigIsDefault
	ReasonModelConfigIsFallback
	ReasonModelConfigNotFound
	ReasonModelConfigsFull
	ReasonModelSnapshotInvalid
	ReasonModelSnapshotMissing
	ReasonNotDirectory
	ReasonNotImage
	ReasonNotRegularFile
	ReasonNothingToNumber
	ReasonNotificationConfigInvalid
	ReasonNotificationCredentialsUnreadable
	ReasonNotificationFailed
	ReasonNotificationNotConfigured
	ReasonNumberingEvidenceUnsaved
	ReasonNumberingHardLink
	ReasonNumberingIndexIncomplete
	ReasonNumberingItemAmbiguous
	ReasonNumberingItemBusy
	ReasonNumberingItemChanged
	ReasonNumberingItemChangedSince
	ReasonNumberingItemMarked
	ReasonNumberingItemModified
	ReasonNumberingItemRenamed
	ReasonNumberingJobGone
	ReasonNumberingJobsFull
	ReasonNumberingMediaChanged
	ReasonNumberingNotPreview
	ReasonNumberingNotRestorable
	ReasonNumberingNothingSelected
	ReasonNumberingNothingToRestore
	ReasonNumberingOriginalUnavailable
	ReasonNumberingPending
	ReasonNumberingRunning
	ReasonNumberingTempConflict
	ReasonNumberingUnconfirmed
	ReasonOperationKeyConflict
	ReasonOperationNotCommitted
	ReasonPreviewNotReady
	ReasonPreviewStale
	ReasonPromptChanged
	ReasonPromptInUse
	ReasonPromptNotFound
	ReasonPromptsFull
	ReasonProtectedEntry
	ReasonRenameParentChanged
	ReasonRenameResultChanged
	ReasonRenameSyncFailed
	ReasonRenameUnconfirmed
	ReasonRestoreNotLatest
	ReasonRestoreSourceChanged
	ReasonRetryOperationMissing
	ReasonRetryOperationStale
	ReasonRetryScopeInvalid
	ReasonReviewPending
	ReasonRootUnavailable
	ReasonRunActive
	ReasonRunNotPausable
	ReasonRunNotPaused
	ReasonRunOverlap
	ReasonRunTargetsUnavailable
	ReasonSamplingSnapshotInvalid
	ReasonScanChanged
	ReasonScanClosed
	ReasonScanHierarchyChanged
	ReasonScanSettingsUnreadable
	ReasonScanTargetUnavailable
	ReasonScanTooDeep
	ReasonScheduleChanged
	ReasonSchedulesFull
	ReasonSecretInvalid
	ReasonSecretUnreadable
	ReasonSkipUnstable
	ReasonSourceUnreadable
	ReasonSymlinkNotAllowed
	ReasonTargetNotRunnable
	ReasonTempCapacityTooSmall
	ReasonTooManyConflicts
	ReasonUnsupportedChannel
	ReasonWaitingRunsFull
)

// Failure is the description attached to a domain reason.
type Failure struct {
	Reason  FailureReason
	Message string
	Details map[string]any
}

// outcomeCodes encode domain diagnostics stored in SQLite. HTTP mapping is owned
// independently by internal/app and never inferred from a persisted record.
var outcomeCodes = map[FailureReason]string{
	ReasonAlreadyProcessed:                  "already_processed",
	ReasonCacheClearIncomplete:              "cache_clear_incomplete",
	ReasonDeleteIncomplete:                  "delete_incomplete",
	ReasonDeleteIndexFailed:                 "delete_index_failed",
	ReasonDeleteListingFailed:               "delete_listing_failed",
	ReasonDescendantBusy:                    "descendant_busy",
	ReasonDirectoryChanged:                  "directory_changed",
	ReasonDiscoveryCursorInvalid:            "discovery_cursor_invalid",
	ReasonDiscoveryHttpError:                "discovery_http_error",
	ReasonDiscoveryInvalid:                  "discovery_invalid",
	ReasonDiscoveryReadFailed:               "discovery_read_failed",
	ReasonDiscoveryRequestInvalid:           "discovery_request_invalid",
	ReasonDiscoveryTimeout:                  "discovery_timeout",
	ReasonDiscoveryTooLarge:                 "discovery_too_large",
	ReasonDiscoveryUnreachable:              "discovery_unreachable",
	ReasonEntryBusy:                         "entry_busy",
	ReasonEntryChanged:                      "entry_changed",
	ReasonEntryNotIndexed:                   "entry_not_indexed",
	ReasonFallbackModelInvalid:              "fallback_model_invalid",
	ReasonFallbackModelUnavailable:          "fallback_model_unavailable",
	ReasonFramesOverReservation:             "frames_over_reservation",
	ReasonIdempotencyCapacity:               "idempotency_capacity",
	ReasonIdempotencyKeyReused:              "idempotency_key_reused",
	ReasonIdempotencyPending:                "idempotency_pending",
	ReasonIdentityChanged:                   "identity_changed",
	ReasonIllegalPath:                       "illegal_path",
	ReasonInvalidCredentials:                "invalid_credentials",
	ReasonInvalidHierarchy:                  "invalid_hierarchy",
	ReasonJournalSourceChanged:              "journal_source_changed",
	ReasonLibraryHalted:                     "library_halted",
	ReasonMarkBatchFailed:                   "mark_batch_failed",
	ReasonMarkResultGone:                    "mark_result_gone",
	ReasonMarkRunning:                       "mark_running",
	ReasonModelConfigChanged:                "model_config_changed",
	ReasonModelConfigInUse:                  "model_config_in_use",
	ReasonModelConfigIncomplete:             "model_config_incomplete",
	ReasonModelConfigIsDefault:              "model_config_is_default",
	ReasonModelConfigIsFallback:             "model_config_is_fallback",
	ReasonModelConfigNotFound:               "model_config_not_found",
	ReasonModelConfigsFull:                  "model_configs_full",
	ReasonModelSnapshotInvalid:              "model_snapshot_invalid",
	ReasonModelSnapshotMissing:              "model_snapshot_missing",
	ReasonNotDirectory:                      "not_directory",
	ReasonNotImage:                          "not_image",
	ReasonNotRegularFile:                    "not_regular_file",
	ReasonNothingToNumber:                   "nothing_to_number",
	ReasonNotificationConfigInvalid:         "notification_config_invalid",
	ReasonNotificationCredentialsUnreadable: "notification_credentials_unreadable",
	ReasonNotificationFailed:                "notification_failed",
	ReasonNotificationNotConfigured:         "notification_not_configured",
	ReasonNumberingEvidenceUnsaved:          "numbering_evidence_unsaved",
	ReasonNumberingHardLink:                 "numbering_hard_link",
	ReasonNumberingIndexIncomplete:          "numbering_index_incomplete",
	ReasonNumberingItemAmbiguous:            "numbering_item_ambiguous",
	ReasonNumberingItemBusy:                 "numbering_item_busy",
	ReasonNumberingItemChanged:              "numbering_item_changed",
	ReasonNumberingItemChangedSince:         "numbering_item_changed_since",
	ReasonNumberingItemMarked:               "numbering_item_marked",
	ReasonNumberingItemModified:             "numbering_item_modified",
	ReasonNumberingItemRenamed:              "numbering_item_renamed",
	ReasonNumberingJobGone:                  "numbering_job_gone",
	ReasonNumberingJobsFull:                 "numbering_jobs_full",
	ReasonNumberingMediaChanged:             "numbering_media_changed",
	ReasonNumberingNotPreview:               "numbering_not_preview",
	ReasonNumberingNotRestorable:            "numbering_not_restorable",
	ReasonNumberingNothingSelected:          "numbering_nothing_selected",
	ReasonNumberingNothingToRestore:         "numbering_nothing_to_restore",
	ReasonNumberingOriginalUnavailable:      "numbering_original_unavailable",
	ReasonNumberingPending:                  "numbering_pending",
	ReasonNumberingRunning:                  "numbering_running",
	ReasonNumberingTempConflict:             "numbering_temp_conflict",
	ReasonNumberingUnconfirmed:              "numbering_unconfirmed",
	ReasonOperationKeyConflict:              "operation_key_conflict",
	ReasonOperationNotCommitted:             "operation_not_committed",
	ReasonPreviewNotReady:                   "preview_not_ready",
	ReasonPreviewStale:                      "preview_stale",
	ReasonPromptChanged:                     "prompt_changed",
	ReasonPromptInUse:                       "prompt_in_use",
	ReasonPromptNotFound:                    "prompt_not_found",
	ReasonPromptsFull:                       "prompts_full",
	ReasonProtectedEntry:                    "protected_entry",
	ReasonRenameParentChanged:               "rename_parent_changed",
	ReasonRenameResultChanged:               "rename_result_changed",
	ReasonRenameSyncFailed:                  "rename_sync_failed",
	ReasonRenameUnconfirmed:                 "rename_unconfirmed",
	ReasonRestoreNotLatest:                  "restore_not_latest",
	ReasonRestoreSourceChanged:              "restore_source_changed",
	ReasonRetryOperationMissing:             "retry_operation_missing",
	ReasonRetryOperationStale:               "retry_operation_stale",
	ReasonRetryScopeInvalid:                 "retry_scope_invalid",
	ReasonReviewPending:                     "review_pending",
	ReasonRootUnavailable:                   "root_unavailable",
	ReasonRunActive:                         "run_active",
	ReasonRunNotPausable:                    "run_not_pausable",
	ReasonRunNotPaused:                      "run_not_paused",
	ReasonRunOverlap:                        "run_overlap",
	ReasonRunTargetsUnavailable:             "run_targets_unavailable",
	ReasonSamplingSnapshotInvalid:           "sampling_snapshot_invalid",
	ReasonScanChanged:                       "scan_changed",
	ReasonScanClosed:                        "scan_closed",
	ReasonScanHierarchyChanged:              "scan_hierarchy_changed",
	ReasonScanSettingsUnreadable:            "scan_settings_unreadable",
	ReasonScanTargetUnavailable:             "scan_target_unavailable",
	ReasonScanTooDeep:                       "scan_too_deep",
	ReasonScheduleChanged:                   "schedule_changed",
	ReasonSchedulesFull:                     "schedules_full",
	ReasonSecretInvalid:                     "secret_invalid",
	ReasonSecretUnreadable:                  "secret_unreadable",
	ReasonSkipUnstable:                      "skip_unstable",
	ReasonSourceUnreadable:                  "source_unreadable",
	ReasonSymlinkNotAllowed:                 "symlink_not_allowed",
	ReasonTargetNotRunnable:                 "target_not_runnable",
	ReasonTempCapacityTooSmall:              "temp_capacity_too_small",
	ReasonTooManyConflicts:                  "too_many_conflicts",
	ReasonUnsupportedChannel:                "unsupported_channel",
	ReasonWaitingRunsFull:                   "waiting_runs_full",
}
