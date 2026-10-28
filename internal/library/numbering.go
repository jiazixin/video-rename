package library

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"video-rename/internal/domain"
)

// NumberingItem is one file in a numbering plan.
type NumberingItem struct {
	Entry      int64  `json:"entry"`
	Old        string `json:"old_name"`
	New        string `json:"new_name"`
	Version    int64  `json:"-"`
	Identity   string `json:"-"`
	Size       int64  `json:"-"`
	ModifiedNS int64  `json:"-"`
	Temp       string `json:"-"`
	Current    string `json:"-"`
	Pending    string `json:"-"`
	Operation  int64  `json:"-"`
	UndoOf     int64  `json:"-"`
}

func numberingToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Numbering runs per media class with identical rules; only the file filter
// and the user-facing noun differ.
type mediaClass struct {
	value string
	match func(name string) bool
}

var numberingMedia = map[string]mediaClass{
	"image": {"image", IsImage},
	"audio": {"audio", IsAudio},
	"mixed": {"mixed", func(name string) bool { return IsImage(name) || IsAudio(name) }},
}

func mediaOf(value string) (mediaClass, error) {
	m, ok := numberingMedia[value]
	if !ok {
		return mediaClass{}, domain.InvalidField("media", "invalid_choice")
	}
	return m, nil
}

// Saved file snapshots can explain a fingerprint mismatch without relaxing
// identity checks on network filesystems. Limit diagnostics for large batches.
func numberingFingerprintFailure(path string, b Numbering, actual string) error {
	var detail strings.Builder
	fmt.Fprintf(&detail, "numbering directory check\ndirectory: %q\npreviewed: %s\npreview fingerprint: %s\ncurrent fingerprint: %s\n", path, b.Created, numberingFingerprintLabel(b.Fingerprint), numberingFingerprintLabel(actual))
	if !describeNumberingSnapshotDiff(&detail, b.Fingerprint, actual) {
		fmt.Fprintln(&detail, "preview format is invalid; directory snapshots cannot be compared. No file in this directory was renamed; preview again.")
		return &domain.JobError{Code: domain.CodeSourceChanged, Message: "numbering preview format is invalid", Details: map[string]any{"reason": "preview_invalid", "path": path}, ProcessingLog: detail.String()}
	}
	fmt.Fprintln(&detail, "No file in this directory was renamed. The differences above are between two reads and do not establish the cause; check the entries and preview again.")
	return &domain.JobError{Code: domain.CodeSourceChanged, Message: "numbering preview check failed", Details: map[string]any{"reason": "preview_mismatch", "path": path}, ProcessingLog: detail.String()}
}

func (l *Library) checkNumberingAvailable(ctx context.Context, id int64) error {
	var pending bool
	if err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM image_numberings WHERE status IN ('running','rollback','review'))").Scan(&pending)
	}); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	var n int
	err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1), ancestors(id,parent) AS (SELECT id,parent FROM entries WHERE id=? UNION ALL SELECT e.id,e.parent FROM entries e JOIN ancestors a ON e.id=a.parent)
 SELECT count(*) FROM image_numberings WHERE status IN ('running','rollback','review') AND (parent IN (SELECT id FROM descendants) OR parent IN (SELECT id FROM ancestors))`, id, id).Scan(&n)
	})
	if err != nil {
		return err
	}
	if n > 0 {
		return domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_pending", Message: "directory has unfinished numbering; recover it from the numbering records"})
	}
	return nil
}

func (l *Library) lockNumbering(ctx context.Context) error {
	for !l.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	if err := ctx.Err(); err != nil {
		l.mu.Unlock()
		return err
	}
	return nil
}

func numberingMatch(path string, i NumberingItem) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	s, err := domain.SnapshotFromInfo(info)
	return err == nil && Identity(s) == i.Identity && s.Size == i.Size && stamp(s) == i.ModifiedNS
}
