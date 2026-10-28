package library

import (
	"errors"
	"fmt"
	"maps"

	"video-rename/internal/domain"
)

// Failures shared by library operations. Codes are listed in docs/API.md.
var (
	errBusy            = domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonEntryBusy, Message: "file or directory has an active job"})
	errChanged         = domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonEntryChanged, Message: "file changed; refresh and retry"})
	errIdentityChanged = domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonIdentityChanged, Message: "file identity changed; refresh the directory"})
	errBadHierarchy    = domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonInvalidHierarchy, Message: "directory hierarchy is invalid"})
	errNotDirectory    = domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonNotDirectory, Message: "entry is not a directory"})
	errNotRegular      = domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonNotRegularFile, Message: "entry is not a regular file"})
	errIllegalPath     = domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonIllegalPath, Message: "path is not allowed"})
	errSymlink         = domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonSymlinkNotAllowed, Message: "symbolic links and special files are not allowed"})
	errProtectedEntry  = domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonProtectedEntry, Message: "the media root, symbolic links and special files cannot be changed"})
	errOperationKey    = domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonOperationKeyConflict, Message: "request key belongs to another operation or one that did not succeed"})
	errPendingReview   = domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonReviewPending, Message: "renames in this scope wait for review"})
	errHalted          = domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonLibraryHalted, Message: "file operations stopped; restart to recover"})
	errRootUnavailable = domain.Fail(domain.KindUnavailable, domain.Failure{Reason: domain.ReasonRootUnavailable, Message: "media root is unavailable or its mount changed"})
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
