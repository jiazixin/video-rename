package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

const (
	refreshPageSize        = 128
	refreshMetadataWorkers = 4
)

// RefreshMetrics records wall time; parallel metadata calls are not added together.
// ReadDir can itself fetch metadata, so both read stages reflect filesystem work.
type RefreshMetrics struct {
	ReadDirectoryDuration time.Duration
	MetadataDuration      time.Duration
	IndexDuration         time.Duration
}

func (l *Library) Refresh(ctx context.Context, id int64) error {
	_, err := l.refreshWithMetrics(ctx, id)
	return err
}

func (l *Library) refreshWithMetrics(ctx context.Context, id int64) (metrics RefreshMetrics, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return
	}
	if err = l.checkNumberingAvailable(id); err != nil {
		return
	}
	e, err := l.indexedSnapshot(id)
	if err != nil {
		return metrics, err
	}
	p, err := l.pathForEntry(e)
	if err != nil {
		return metrics, err
	}
	if e.Kind != "directory" {
		return metrics, fmt.Errorf("不是目录")
	}
	f, err := os.Open(p)
	if err != nil {
		return metrics, err
	}
	defer f.Close()
	err = l.refreshDirectoryWithMetrics(ctx, id, f.ReadDir, &metrics)
	return
}

// refreshDirectory requires l.mu to keep application mutations out of the scan.
func (l *Library) refreshDirectory(ctx context.Context, id int64, readDir func(int) ([]os.DirEntry, error)) error {
	var metrics RefreshMetrics
	return l.refreshDirectoryWithMetrics(ctx, id, readDir, &metrics)
}

func (l *Library) refreshDirectoryWithMetrics(ctx context.Context, id int64, readDir func(int) ([]os.DirEntry, error), metrics *RefreshMetrics) error {
	gen := time.Now().UnixNano()
	fail := func(err error) error {
		start := time.Now()
		_, _ = l.DB.Exec("INSERT INTO directory_state(dir,error) VALUES(?,?) ON CONFLICT(dir) DO UPDATE SET error=excluded.error", id, err.Error())
		metrics.IndexDuration += time.Since(start)
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		start := time.Now()
		batch, readErr := readDir(refreshPageSize)
		metrics.ReadDirectoryDuration += time.Since(start)
		// No writer connection or transaction is held during filesystem reads.
		start = time.Now()
		scanned, err := refreshMetadata(ctx, batch)
		metrics.MetadataDuration += time.Since(start)
		if err != nil {
			return fail(err)
		}
		if len(scanned) > 0 {
			start = time.Now()
			err = l.indexRefreshPage(ctx, id, gen, scanned)
			metrics.IndexDuration += time.Since(start)
			if err != nil {
				return fail(err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fail(readErr)
		}
	}
	start := time.Now()
	err := l.publishRefresh(ctx, id, gen)
	metrics.IndexDuration += time.Since(start)
	if err != nil {
		// publishRefresh closes its transaction before fail uses the sole writer.
		return fail(err)
	}
	return nil
}

type scannedDirectoryEntry struct {
	name, kind, identity, mtime string
	size                        int64
}

// refreshMetadata joins every worker before returning, including on error or
// cancellation. DirEntry.Info cannot interrupt an in-flight filesystem syscall.
// Each worker owns a distinct result slot; persistence keeps enumeration order.
func refreshMetadata(ctx context.Context, batch []os.DirEntry) ([]scannedDirectoryEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scanned := make([]scannedDirectoryEntry, len(batch))
	errorsByIndex := make([]error, len(batch))
	var next atomic.Int64
	var firstFailure atomic.Int64
	firstFailure.Store(int64(len(batch)))
	var workers sync.WaitGroup
	for range min(refreshMetadataWorkers, len(batch)) {
		workers.Go(func() {
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= len(batch) || int64(i) > firstFailure.Load() || ctx.Err() != nil {
					return
				}
				de := batch[i]
				info, err := de.Info()
				if err == nil {
					var snap domain.Snapshot
					snap, err = domain.SnapshotFromInfo(info)
					if err == nil {
						scanned[i] = scannedDirectoryEntry{de.Name(), kindOf(info), Identity(snap), stamp(snap), snap.Size}
					}
				}
				if err != nil {
					errorsByIndex[i] = err
					// Stop later work but finish every earlier reserved index,
					// so the reported error does not depend on completion order.
					for previous := firstFailure.Load(); int64(i) < previous; previous = firstFailure.Load() {
						if firstFailure.CompareAndSwap(previous, int64(i)) {
							break
						}
					}
					return
				}
			}
		})
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, err := range errorsByIndex {
		if err != nil {
			return nil, err
		}
	}
	return scanned, nil
}

func (l *Library) indexRefreshPage(ctx context.Context, id, gen int64, scanned []scannedDirectoryEntry) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := make(map[string]*sql.Stmt)
	defer func() {
		for _, statement := range statements {
			statement.Close()
		}
	}()
	prepare := func(query string) (*sql.Stmt, error) {
		if statement := statements[query]; statement != nil {
			return statement, nil
		}
		statement, err := tx.PrepareContext(ctx, query)
		if err == nil {
			statements[query] = statement
		}
		return statement, err
	}
	lookup, err := prepare("SELECT id,identity FROM entries WHERE parent=? AND name=? AND present=1")
	if err != nil {
		return err
	}
	insertItem, err := prepare("INSERT INTO directory_items VALUES(?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	for _, item := range scanned {
		var eid int64
		var identity string
		err := lookup.QueryRowContext(ctx, id, item.name).Scan(&eid, &identity)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && identity != item.identity {
			statement, err := prepare("UPDATE entries SET present=0 WHERE id=?")
			if err != nil {
				return err
			}
			if _, err = statement.ExecContext(ctx, eid); err != nil {
				return err
			}
			eid = 0
		}
		if eid == 0 {
			statement, err := prepare("INSERT INTO entries(parent,name,kind,identity,size,mtime,observed) VALUES(?,?,?,?,?,?,?)")
			if err != nil {
				return err
			}
			res, err := statement.ExecContext(ctx, id, item.name, item.kind, item.identity, item.size, item.mtime, storage.Now())
			if err != nil {
				return err
			}
			eid, err = res.LastInsertId()
			if err != nil {
				return err
			}
		} else {
			statement, err := prepare("UPDATE entries SET version=version+CASE WHEN size<>? OR mtime<>? THEN 1 ELSE 0 END,size=?,mtime=?,kind=?,observed=? WHERE id=?")
			if err != nil {
				return err
			}
			if _, err = statement.ExecContext(ctx, item.size, item.mtime, item.size, item.mtime, item.kind, storage.Now(), eid); err != nil {
				return err
			}
		}
		if _, err = insertItem.ExecContext(ctx, id, gen, eid, item.name, item.kind, item.size, item.mtime); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func (l *Library) publishRefresh(ctx context.Context, id, gen int64) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE entries SET present=0 WHERE parent=? AND id NOT IN(SELECT entry FROM directory_items WHERE dir=? AND generation=?)", id, id, gen); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO directory_state(dir,generation,refreshed,error) VALUES(?,?,?,'') ON CONFLICT(dir) DO UPDATE SET generation=excluded.generation,refreshed=excluded.refreshed,error=''", id, gen, storage.Now()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM directory_items WHERE dir=? AND generation<>?", id, gen); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}
