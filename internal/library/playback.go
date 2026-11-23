package library

import (
	"context"
	"os"

	"video-rename/internal/domain"
)

// OpenPlayback pins a regular file beneath the configured root. The returned
// descriptor stays valid across later renames; callers must close it.
func (l *Library) OpenPlayback(ctx context.Context, id int64) (*os.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, err := l.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if e.Kind != "file" {
		return nil, errNotRegular
	}
	p, err := l.Path(ctx, id)
	if err != nil {
		return nil, err
	}
	mount, rel, err := l.rootPath(p)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(mount.Path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	snap, err := domain.SnapshotFromInfo(st)
	if !st.Mode().IsRegular() || err != nil || !mount.matches(snap, e.Identity) {
		f.Close()
		return nil, errIdentityChanged
	}
	// Recheck ancestors after opening, including root mount and symlink changes.
	if _, err = l.Path(ctx, id); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
