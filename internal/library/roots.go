package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// MediaRoot identifies a configured media tree. Only the display label is exposed over HTTP.
// dev and ino hold the runtime mount identity observed at startup.
type MediaRoot struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"-"`
	dev  uint64
	ino  uint64
}

// MediaRoots returns the roots in configuration order.
func (l *Library) MediaRoots() []MediaRoot { return slices.Clone(l.roots) }

func (r MediaRoot) matches(s domain.Snapshot, identity string) bool {
	return s.Dev == r.dev && EntryIdentity(r.ID, s.Ino) == identity
}

func (r MediaRoot) matchesRoot(s domain.Snapshot, identity string) bool {
	return s.Dev == r.dev && s.Ino == r.ino && EntryIdentity(r.ID, s.Ino) == identity
}

func (r MediaRoot) entryIdentity(s domain.Snapshot) (string, bool) {
	if s.Dev != r.dev {
		return "", false
	}
	return EntryIdentity(r.ID, s.Ino), true
}

func (r MediaRoot) identityLabel(s domain.Snapshot) string {
	if id, ok := r.entryIdentity(s); ok {
		return fmt.Sprintf("%s (dev=%d)", id, s.Dev)
	}
	return crossDeviceIdentity(s)
}

type rootObservation struct {
	snap       domain.Snapshot
	mountPoint bool
}

func inspectRoot(path string) (rootObservation, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return rootObservation{}, fmt.Errorf("invalid media directory %q", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return rootObservation{}, err
	}
	if !info.IsDir() {
		return rootObservation{}, fmt.Errorf("invalid media directory %q", path)
	}
	snap, err := domain.SnapshotFromInfo(info)
	if err != nil {
		return rootObservation{}, err
	}
	mountPoint := false
	parent := filepath.Dir(path)
	if parent != path {
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return rootObservation{}, fmt.Errorf("inspect parent of media directory %q: %w", path, err)
		}
		parentSnap, err := domain.SnapshotFromInfo(parentInfo)
		if err != nil {
			return rootObservation{}, err
		}
		mountPoint = snap.Dev != parentSnap.Dev
	}
	return rootObservation{snap: snap, mountPoint: mountPoint}, nil
}

func bindRoots(ctx context.Context, db *storage.Store, paths []string) ([]MediaRoot, error) {
	return bindRootsWith(ctx, db, paths, inspectRoot)
}

func bindRootsWith(ctx context.Context, db *storage.Store, paths []string, inspect func(string) (rootObservation, error)) ([]MediaRoot, error) {
	if len(paths) == 0 {
		return nil, errors.New("media_dir requires at least one directory")
	}
	type runtimeKey struct {
		dev uint64
		ino uint64
	}
	observed := make(map[string]rootObservation, len(paths))
	names := make(map[string]int, len(paths))
	runtimeIdentities := make(map[runtimeKey]bool, len(paths))
	for _, path := range paths {
		obs, err := inspect(path)
		if err != nil {
			return nil, err
		}
		key := runtimeKey{dev: obs.snap.Dev, ino: obs.snap.Ino}
		if runtimeIdentities[key] {
			return nil, fmt.Errorf("duplicate media directory %q", path)
		}
		runtimeIdentities[key] = true
		observed[path] = obs
		names[filepath.Base(path)]++
	}

	return storage.WriteValue(ctx, db, func(tx *sql.Tx) ([]MediaRoot, error) {
		type binding struct {
			path       string
			identity   string
			id         int64
			mountPoint int
		}
		rows, err := tx.QueryContext(ctx, "SELECT l.root,l.mount_point,e.identity,e.id FROM library l JOIN entries e ON e.id=l.id ORDER BY l.id")
		if err != nil {
			return nil, err
		}
		bound, err := storage.CollectRows(rows, func(r *sql.Rows) (b binding, err error) {
			err = r.Scan(&b.path, &b.mountPoint, &b.identity, &b.id)
			return b, err
		})
		if err != nil {
			return nil, err
		}
		ids := make(map[string]int64, len(bound))
		for _, b := range bound {
			obs, exists := observed[b.path]
			if !exists {
				return nil, fmt.Errorf("bound media directory %q was removed from media_dir; restore media_dir or use a new data_dir", b.path)
			}
			wantMount := b.mountPoint != 0
			gotIdentity := EntryIdentity(b.id, obs.snap.Ino)
			if obs.mountPoint != wantMount {
				return nil, fmt.Errorf("bound media directory %q mount state changed (expected mount_point=%t, got %t; expected identity %s, got %s on dev %d); check mount or use a new data_dir", b.path, wantMount, obs.mountPoint, b.identity, gotIdentity, obs.snap.Dev)
			}
			if gotIdentity != b.identity {
				return nil, fmt.Errorf("bound media directory %q identity changed (expected %s, got %s on dev %d); check media_dir or mount options (such as serverino), or use a new data_dir", b.path, b.identity, gotIdentity, obs.snap.Dev)
			}
			ids[b.path] = b.id
		}
		roots := make([]MediaRoot, 0, len(paths))
		for _, path := range paths {
			name := filepath.Base(path)
			if names[name] > 1 {
				name = strings.TrimPrefix(path, "/")
				if !strings.Contains(name, "/") {
					name = path
				}
			}
			entryName := name
			if len(paths) == 1 {
				entryName = ""
			}
			obs := observed[path]
			id := ids[path]
			if id == 0 {
				err = tx.QueryRowContext(ctx, "INSERT INTO entries(parent,name,kind,identity,size,mtime_ns,observed) VALUES(0,?,'directory','',?,?,?) RETURNING id", entryName, obs.snap.Size, stamp(obs.snap), storage.Now()).Scan(&id)
				if err != nil {
					return nil, err
				}
				if _, err = tx.ExecContext(ctx, "UPDATE entries SET identity=? WHERE id=?", EntryIdentity(id, obs.snap.Ino), id); err != nil {
					return nil, err
				}
				mountFlag := 0
				if obs.mountPoint {
					mountFlag = 1
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO library(id,root,mount_point) VALUES(?,?,?)", id, path, mountFlag); err != nil {
					return nil, err
				}
			} else {
				if _, err := tx.ExecContext(ctx, "UPDATE entries SET name=? WHERE id=? AND name<>?", entryName, id, entryName); err != nil {
					return nil, err
				}
			}
			roots = append(roots, MediaRoot{ID: id, Path: path, Name: name, dev: obs.snap.Dev, ino: obs.snap.Ino})
		}
		return roots, nil
	})
}

// rootPath resolves a verified absolute path to its owning root and relative path.
func (l *Library) rootPath(path string) (MediaRoot, string, error) {
	for _, root := range l.roots {
		rel, err := filepath.Rel(root.Path, path)
		if err == nil && filepath.IsLocal(rel) {
			return root, rel, nil
		}
	}
	return MediaRoot{}, "", errIllegalPath
}

// RelativePath returns the unambiguous display path for a verified filesystem path.
func (l *Library) RelativePath(path string) (string, error) {
	root, rel, err := l.rootPath(path)
	if err == nil && len(l.roots) > 1 {
		rel = filepath.Join(root.Name, rel)
	}
	return rel, err
}

// RootID finds the configured root containing an indexed entry.
func (l *Library) RootID(ctx context.Context, id int64) (int64, error) {
	if len(l.roots) == 1 {
		return l.roots[0].ID, nil
	}
	for depth := 0; id > 0 && depth <= 1024; depth++ {
		if _, ok := l.byRootID[id]; ok {
			return id, nil
		}
		e, err := l.IndexedEntry(ctx, id)
		if err != nil {
			return 0, err
		}
		id = e.Parent
	}
	return 0, errBadHierarchy
}
