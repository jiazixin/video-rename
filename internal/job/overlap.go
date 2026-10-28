package job

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"maps"
	"slices"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

type scopeTarget struct {
	id, parent int64
	kind       string
	name       string
	identity   string
}

// scopeIndex lives for one Create call. Only selected/active targets and their
// ancestors are loaded; descendants, run status and rename history are not read.
type scopeIndex struct {
	db        *storage.Store
	entries   map[int64]scopeTarget
	ancestors map[int64]map[int64]bool
}

func newScopeIndex(db *storage.Store) *scopeIndex {
	return &scopeIndex{db: db, entries: map[int64]scopeTarget{}, ancestors: map[int64]map[int64]bool{}}
}

func (s *scopeIndex) get(ctx context.Context, id int64) (scopeTarget, error) {
	if e, ok := s.entries[id]; ok {
		return e, nil
	}
	var e scopeTarget
	err := s.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT id,parent,kind,identity,name FROM entries WHERE id=? AND present=1", id).Scan(&e.id, &e.parent, &e.kind, &e.identity, &e.name)
	})
	if err == nil {
		s.entries[id] = e
	}
	return e, err
}

func (s *scopeIndex) parents(ctx context.Context, id int64) (map[int64]bool, error) {
	if parents, ok := s.ancestors[id]; ok {
		return parents, nil
	}
	parents := map[int64]bool{}
	for current := id; current > 0; {
		if parents[current] || len(parents) >= 1024 {
			return nil, domain.Fail(domain.KindConflict, domain.Record{Code: "invalid_hierarchy", Message: "directory hierarchy is invalid"})
		}
		parents[current] = true
		e, err := s.get(ctx, current)
		if err != nil {
			return nil, err
		}
		current = e.parent
	}
	s.ancestors[id] = parents
	return parents, nil
}

func (s *scopeIndex) normalize(ctx context.Context, ids []int64, recursive bool) ([]int64, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if !recursive {
		return ids, nil
	}
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		parents, err := s.parents(ctx, id)
		if err != nil {
			return nil, err
		}
		covered := false
		for ancestor := range maps.Keys(parents) {
			if ancestor != id && selected[ancestor] && s.entries[ancestor].kind == "directory" {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, id)
		}
	}
	return result, nil
}

func (s *scopeIndex) covers(ctx context.Context, a, b scopeTarget, recursive bool) (bool, error) {
	if a.id == b.id || a.kind == "file" && b.kind == "file" && a.identity == b.identity {
		return true, nil
	}
	if a.kind != "directory" {
		return false, nil
	}
	if !recursive {
		return b.kind == "file" && b.parent == a.id, nil
	}
	parents, err := s.parents(ctx, b.id)
	return parents[a.id], err
}

// All callers create batches through Create's mutex, including scheduled runs.
func (m *Manager) checkOverlap(ctx context.Context, request Request, index *scopeIndex) error {
	type scope struct {
		id        int64
		targets   []int64
		recursive bool
	}
	var scopes []scope
	err := m.db.View(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id,targets,recursive FROM runs WHERE status IN "+domain.ActiveRunStatusesSQL+" ORDER BY id")
		if err != nil {
			return err
		}
		scopes, err = storage.CollectRows(rows, func(rows *sql.Rows) (scope, error) {
			var s scope
			var raw string
			if err := rows.Scan(&s.id, &raw, &s.recursive); err != nil {
				return s, err
			}
			err := json.Unmarshal([]byte(raw), &s.targets)
			return s, err
		})
		return err
	})
	if err != nil {
		return err
	}
	// Reuse the normalization cache for both targets and ancestor relationships.
	unique := map[int64]struct{}{}
	for _, id := range request.Targets {
		unique[id] = struct{}{}
	}
	for _, s := range scopes {
		for _, id := range s.targets {
			unique[id] = struct{}{}
		}
	}
	for id := range maps.Keys(unique) {
		if _, err := index.get(ctx, id); err != nil {
			for _, s := range scopes {
				if slices.Contains(s.targets, id) {
					return domain.Fail(domain.KindConflict, domain.Record{Code: "run_targets_unavailable", Message: "targets of an unfinished run are unavailable", Details: map[string]any{"run": s.id}})
				}
			}
			return err
		}
	}
	for _, id := range request.Targets {
		a := index.entries[id]
		for _, s := range scopes {
			for _, other := range s.targets {
				covered, err := index.covers(ctx, a, index.entries[other], request.Recursive)
				if err != nil {
					return err
				}
				if !covered {
					covered, err = index.covers(ctx, index.entries[other], a, s.recursive)
					if err != nil {
						return err
					}
				}
				if covered {
					return domain.Fail(domain.KindConflict, domain.Record{Code: "run_overlap", Message: "selection overlaps an unfinished run", Details: map[string]any{"run": s.id}})
				}
			}
		}
	}
	return nil
}
