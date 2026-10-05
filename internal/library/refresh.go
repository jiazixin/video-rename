package library

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	refreshPageSize        = 128
	refreshMetadataWorkers = 4
)

// RefreshMetrics records wall time; parallel metadata calls are not added together.
// ReadDir can itself fetch metadata, so both read stages reflect filesystem work.
// IndexDuration covers the remaining coordination, staging and publication time.
type RefreshMetrics struct {
	ReadDirectoryDuration time.Duration
	MetadataDuration      time.Duration
	IndexDuration         time.Duration
}

func (l *Library) Refresh(ctx context.Context, id int64) error {
	_, err := l.refreshWithMetrics(ctx, id)
	return err
}

func (l *Library) refreshWithMetrics(ctx context.Context, id int64) (RefreshMetrics, error) {
	result, err := l.ScanDirectory(ctx, id)
	return result.Metrics, err
}

type scannedDirectoryEntry struct {
	name, kind, identity, mtime string
	size                        int64
}

// readDirectoryMetadata joins all workers, including on cancellation or failure.
func readDirectoryMetadata(ctx context.Context, batch []os.DirEntry, inspect func(os.DirEntry) (scannedDirectoryEntry, error)) ([]scannedDirectoryEntry, error) {
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
				var err error
				scanned[i], err = inspect(batch[i])
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
