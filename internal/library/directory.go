package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"video-rename/internal/domain"
)

// openDirectory pins each indexed ancestor before opening its child. A path
// replacement cannot redirect subsequent relative filesystem operations.
// The caller holds Library.mu and must close the returned directory.
func (l *Library) openDirectory(ctx context.Context, id int64) (*os.File, error) {
	var chain []Entry
	seen := map[int64]bool{}
	for {
		if id <= 0 || seen[id] || len(chain) > 1024 {
			return nil, errBadHierarchy
		}
		seen[id] = true
		e, err := l.IndexedEntry(ctx, id)
		if err != nil {
			return nil, err
		}
		if e.Kind != "directory" {
			return nil, errNotDirectory
		}
		if id != 1 && (e.Name == "." || e.Name == ".." || filepath.Base(e.Name) != e.Name || e.Name == "") {
			return nil, errIllegalPath
		}
		chain = append(chain, e)
		if id == 1 {
			break
		}
		id = e.Parent
	}
	var dir *os.File
	for _, e := range slices.Backward(chain) {
		parent, name := unix.AT_FDCWD, l.Root
		if dir != nil {
			parent, name = int(dir.Fd()), e.Name
		}
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if dir != nil {
			dir.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("open media directory: %w", err)
		}
		dir = os.NewFile(uintptr(fd), name)
		info, err := dir.Stat()
		if err == nil {
			var snap domain.Snapshot
			snap, err = domain.SnapshotFromInfo(info)
			if err == nil && Identity(snap) != e.Identity {
				err = errIdentityChanged
			}
		}
		if err != nil {
			dir.Close()
			return nil, err
		}
	}
	return dir, nil
}

func directorySnapshot(dir *os.File, name string) (domain.Snapshot, error) {
	if name == "." || name == ".." || filepath.Base(name) != name || name == "" {
		return domain.Snapshot{}, errIllegalPath
	}
	var st unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return domain.Snapshot{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG && st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return domain.Snapshot{}, errSymlink
	}
	return domain.Snapshot{Dev: uint64(st.Dev), Ino: uint64(st.Ino), Size: st.Size, ModTime: time.Unix(st.Mtim.Sec, st.Mtim.Nsec)}, nil
}
