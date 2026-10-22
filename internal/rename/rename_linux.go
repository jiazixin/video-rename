//go:build linux

// Package rename 封装平台专用的原子 no-replace 重命名。
// 禁止用"检查目标不存在 → 普通 rename"替代:两步间存在竞争。
// Linux 使用 renameat2 的 RENAME_NOREPLACE;内核或文件系统不支持时
// 报 RENAME_UNSUPPORTED 并保留原文件,绝不退化为可能覆盖的操作。
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
		return &domain.JobError{Code: domain.CodeRenameFailed, Message: err.Error()}
	}
	switch {
	case errno == syscall.EEXIST:
		return &domain.JobError{Code: domain.CodeRenameConflict, Message: "目标已存在"}
	case errno == syscall.ENOSYS || errno == syscall.EINVAL || errno == syscall.ENOTSUP || errno == syscall.EOPNOTSUPP:
		return &domain.JobError{
			Code:    domain.CodeRenameUnsupported,
			Message: "内核或文件系统不支持 RENAME_NOREPLACE,保留原文件",
		}
	case errno == syscall.EACCES || errno == syscall.EPERM:
		return &domain.JobError{Code: domain.CodeRenamePermissionDenied, Message: errno.Error()}
	default:
		return &domain.JobError{
			Code:    domain.CodeRenameFailed,
			Message: errno.Error() + " (" + oldname + " → " + newname + ")",
		}
	}
}
