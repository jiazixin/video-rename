//go:build darwin

package domain

import (
	"syscall"
	"time"
)

// StatSnapshot 获取路径的文件身份快照(不跟随符号链接)。
func StatSnapshot(path string) (Snapshot, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Dev:     uint64(st.Dev),
		Ino:     st.Ino,
		Size:    st.Size,
		ModTime: time.Unix(st.Mtimespec.Sec, st.Mtimespec.Nsec),
	}, nil
}
