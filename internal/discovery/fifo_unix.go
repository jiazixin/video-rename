//go:build unix

package discovery

import (
	"syscall"
)

// mkFifo 创建命名管道,用于验证非普通文件被跳过。
func mkFifo(path string) error {
	return syscall.Mkfifo(path, 0o644)
}
