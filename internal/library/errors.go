package library

import (
	"errors"
	"fmt"
	"maps"

	"video-rename/internal/domain"
)

// Failures shared by library operations. Codes are listed in docs/API.md.
var (
	errBusy             = domain.Fail(domain.KindConflict, "entry_busy", "file or directory has an active job")
	errChanged          = domain.Fail(domain.KindConflict, "entry_changed", "file changed; refresh and retry")
	errIdentityChanged  = domain.Fail(domain.KindConflict, "identity_changed", "file identity changed; refresh the directory")
	errBadHierarchy     = domain.Fail(domain.KindConflict, "invalid_hierarchy", "directory hierarchy is invalid")
	errNotDirectory     = domain.Fail(domain.KindInvalid, "not_directory", "entry is not a directory")
	errNotRegular       = domain.Fail(domain.KindInvalid, "not_regular_file", "entry is not a regular file")
	errIllegalPath      = domain.Fail(domain.KindInvalid, "illegal_path", "path is not allowed")
	errSymlink          = domain.Fail(domain.KindInvalid, "symlink_not_allowed", "symbolic links and special files are not allowed")
	errProtectedEntry   = domain.Fail(domain.KindInvalid, "protected_entry", "the media root, symbolic links and special files cannot be changed")
	errOperationKey     = domain.Fail(domain.KindConflict, "operation_key_conflict", "request key belongs to another operation or one that did not succeed")
	errPendingReview    = domain.Fail(domain.KindConflict, "review_pending", "renames in this scope wait for review")
	errHalted           = domain.Fail(domain.KindInternal, "library_halted", "file operations stopped; restart to recover")
	errRootUnavailable  = domain.Fail(domain.KindUnavailable, "root_unavailable", "media root is unavailable or its mount changed")
	errDirectoryRefresh = domain.Fail(domain.KindConflict, "directory_refreshed", "directory was refreshed; return to the first page")
)

// Display paths of the media root and of a missing directory. Neither can be
// a real path, which is relative and never starts with a slash.
const (
	RootDisplayPath    = "/"
	MissingDisplayPath = "/?"
)

// inDirectory adds the directory where err happened to its message and as
// details.directory, so clients can name it. Errors without a code keep the
// directory in their text only.
func inDirectory(err error, dir string) error {
	withDirectory := func(details map[string]any) map[string]any {
		details = maps.Clone(details)
		if details == nil {
			details = map[string]any{}
		}
		details["directory"] = dir
		return details
	}
	if je, ok := errors.AsType[*domain.JobError](err); ok {
		copied := *je
		copied.Details = withDirectory(je.Details)
		copied.Message = dir + ": " + copied.Message
		return &copied
	}
	if e, ok := domain.AsError(err); ok {
		copied := *e
		copied.Details = withDirectory(e.Details)
		copied.Message = dir + ": " + copied.Message
		return &copied
	}
	return fmt.Errorf("%s: %w", dir, err)
}
