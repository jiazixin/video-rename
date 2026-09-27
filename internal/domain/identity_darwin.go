//go:build darwin

package domain

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// StatSnapshot 获取路径的文件身份快照(不跟随符号链接)。
func StatSnapshot(path string) (Snapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Snapshot{}, err
	}
	return SnapshotFromInfo(info)
}

// SnapshotFromInfo reuses an Lstat/DirEntry.Info result without another filesystem read.
func SnapshotFromInfo(info os.FileInfo) (Snapshot, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Snapshot{}, fmt.Errorf("文件信息缺少系统身份")
	}
	return Snapshot{
		Dev:     uint64(st.Dev),
		Ino:     st.Ino,
		Size:    st.Size,
		ModTime: time.Unix(st.Mtimespec.Sec, st.Mtimespec.Nsec),
	}, nil
}
