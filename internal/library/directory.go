package library

import (
	"context"
	"os"
	"path/filepath"
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
		chain = append(chain, e)
		if root, ok := l.byRootID[id]; ok {
			return openScanDirectory(root.Path, chain)
		}
		id = e.Parent
	}
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
