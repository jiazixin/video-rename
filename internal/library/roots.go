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
type MediaRoot struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"-"`
}

// MediaRoots returns the roots in configuration order.
func (l *Library) MediaRoots() []MediaRoot { return slices.Clone(l.roots) }

func bindRoots(ctx context.Context, db *storage.Store, paths []string) ([]MediaRoot, error) {
	if len(paths) == 0 {
		return nil, errors.New("media_dir requires at least one directory")
	}
	snapshots := make(map[string]domain.Snapshot, len(paths))
	names := make(map[string]int, len(paths))
	identities := make(map[string]bool, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || !filepath.IsAbs(path) {
			return nil, fmt.Errorf("invalid media directory %q", path)
		}
		snap, err := domain.SnapshotFromInfo(info)
		if err != nil {
			return nil, err
		}
		if identities[Identity(snap)] {
			return nil, fmt.Errorf("duplicate media directory %q", path)
		}
		identities[Identity(snap)] = true
		snapshots[path] = snap
		names[filepath.Base(path)]++
	}

	return storage.WriteValue(ctx, db, func(tx *sql.Tx) ([]MediaRoot, error) {
		type binding struct {
			path, identity string
			id             int64
		}
		rows, err := tx.QueryContext(ctx, "SELECT l.root,e.identity,e.id FROM library l JOIN entries e ON e.id=l.id")
		if err != nil {
			return nil, err
		}
		bound, err := storage.CollectRows(rows, func(r *sql.Rows) (b binding, err error) {
			err = r.Scan(&b.path, &b.identity, &b.id)
			return b, err
		})
		if err != nil {
			return nil, err
		}
		ids := make(map[string]int64, len(bound))
		for _, b := range bound {
			snap, exists := snapshots[b.path]
			if !exists || Identity(snap) != b.identity {
				return nil, fmt.Errorf("bound media directory %q was removed or its mount changed; check media_dir or use a new data_dir", b.path)
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
			id := ids[path]
			if id == 0 {
				snap := snapshots[path]
				err = tx.QueryRowContext(ctx, "INSERT INTO entries(parent,name,kind,identity,size,mtime_ns,observed) VALUES(0,?,'directory',?,?,?,?) RETURNING id", entryName, Identity(snap), snap.Size, stamp(snap), storage.Now()).Scan(&id)
				if err != nil {
					return nil, err
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO library(id,root) VALUES(?,?)", id, path); err != nil {
					return nil, err
				}
			} else {
				if _, err := tx.ExecContext(ctx, "UPDATE entries SET name=? WHERE id=? AND name<>?", entryName, id, entryName); err != nil {
					return nil, err
				}
			}
			roots = append(roots, MediaRoot{ID: id, Path: path, Name: name})
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
