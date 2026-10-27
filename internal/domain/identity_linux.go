//go:build linux

package domain

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// StatSnapshot returns the file identity snapshot of path without following symlinks.
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
		return Snapshot{}, errors.New("file info has no system identity")
	}
	return Snapshot{
		Dev:     uint64(st.Dev),
		Ino:     uint64(st.Ino),
		Size:    st.Size,
		ModTime: time.Unix(st.Mtim.Sec, st.Mtim.Nsec),
	}, nil
}
