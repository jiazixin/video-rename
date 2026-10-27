package naming

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"video-rename/internal/domain"
)

// Allocator assigns candidate target names within a directory one at a time.
// It tracks names reserved in this run (case-insensitively, to stay safe on case-insensitive volumes)
// and combines the disk state with the source identity to decide unchanged or a conflict suffix.
// It is safe for concurrent use; the commit coordinator calls it serially.
type Allocator struct {
	mu       sync.Mutex
	reserved map[string]map[string]struct{} // dir → set of lowercase names
}

// NewAllocator returns an empty allocator.
func NewAllocator() *Allocator {
	return &Allocator{reserved: map[string]map[string]struct{}{}}
}

// AllocateResult is the result of a candidate allocation.
type AllocateResult struct {
	Target    string // absolute target path
	Unchanged bool   // the target is the source file and its extension is already lowercase
}

// Allocate assigns a target name for a cleaned title.
// On conflict it appends " (2)", " (3)"… before the extension, re-reserving bytes for suffix and extension each time;
// past (9999) it reports rename_conflict_exhausted.
// When a candidate is the source file itself it returns Unchanged, unless the extension case must be normalized.
func (a *Allocator) Allocate(dir, title, ext string, job domain.VideoJob) (AllocateResult, *domain.JobError) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ext = NormalizeExtension(ext)

	for n := 1; n <= 9999; n++ {
		reserve := 0
		if n > 1 {
			reserve = len(fmt.Sprintf(" (%d)", n))
		}
		budget := MaxFilenameBytes - len(ext) - reserve
		t, terr := TruncateToBytes(title, budget, ext)
		if terr != nil {
			return AllocateResult{}, terr
		}
		name := t + ext
		if n > 1 {
			name = t + fmt.Sprintf(" (%d)", n) + ext
		}
		target := filepath.Join(dir, name)

		// candidate exists: skip only for the same identity with no extension change; try the next suffix for another identity.
		if snap, err := domain.StatSnapshot(target); err == nil {
			if snap.SameIdentity(job.Snapshot) && sameDirectoryEntry(dir, name, filepath.Base(job.Path)) {
				return AllocateResult{Target: target, Unchanged: filepath.Ext(job.Path) == ext}, nil
			}
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return AllocateResult{}, &domain.JobError{
				Code:    domain.CodeRenameFailed,
				Message: fmt.Sprintf("check target %s: %v", target, err),
				Details: map[string]any{"to": target, "detail": err.Error()},
			}
		}
		key := strings.ToLower(name)
		if a.reserved[dir] != nil {
			if _, dup := a.reserved[dir][key]; dup {
				continue
			}
		}
		if a.reserved[dir] == nil {
			a.reserved[dir] = map[string]struct{}{}
		}
		a.reserved[dir][key] = struct{}{}
		return AllocateResult{Target: target}, nil
	}
	return AllocateResult{}, &domain.JobError{
		Code:    domain.CodeRenameConflictExhausted,
		Message: fmt.Sprintf("more than 9999 names taken in directory %s", dir),
	}
}

// Release returns a reserved name (used when a target race requires another suffix).
func (a *Allocator) Release(dir, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if m := a.reserved[dir]; m != nil {
		delete(m, strings.ToLower(name))
	}
}

// Equal identities can also be separate hard links. Only accept an exact source
// name or a case alias that does not exist as a separate directory entry.
func sameDirectoryEntry(dir, target, source string) bool {
	if target == source {
		return true
	}
	if !strings.EqualFold(target, source) {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() == target {
			return false
		}
	}
	return true
}
