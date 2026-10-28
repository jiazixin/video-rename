package rename

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"

	"video-rename/internal/domain"
)

// NoReplaceAt renames two basenames within an already verified directory and
// never replaces an existing target. Callers open the directory first, so the
// rename cannot follow a path component swapped for a symlink.
func NoReplaceAt(dirfd int, oldname, newname string) *domain.JobError {
	return mapErrno(unix.Renameat2(dirfd, oldname, dirfd, newname, unix.RENAME_NOREPLACE), oldname, newname)
}

func mapErrno(err error, oldname, newname string) *domain.JobError {
	if err == nil {
		return nil
	}
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		return &domain.JobError{Code: domain.CodeRenameFailed, Message: err.Error(), Details: map[string]any{"detail": err.Error()}}
	}
	switch {
	case errno == syscall.EEXIST:
		return &domain.JobError{Code: domain.CodeRenameConflict, Message: "target exists"}
	case errno == syscall.ENOSYS || errno == syscall.EINVAL || errno == syscall.ENOTSUP || errno == syscall.EOPNOTSUPP:
		return &domain.JobError{
			Code:    domain.CodeRenameUnsupported,
			Message: "kernel or file system does not support RENAME_NOREPLACE; original file kept",
		}
	case errno == syscall.EACCES || errno == syscall.EPERM:
		return &domain.JobError{Code: domain.CodeRenamePermissionDenied, Message: errno.Error(), Details: map[string]any{"detail": errno.Error()}}
	default:
		return &domain.JobError{
			Code:    domain.CodeRenameFailed,
			Message: errno.Error() + " (" + oldname + " -> " + newname + ")",
			Details: map[string]any{"detail": errno.Error()},
		}
	}
}
