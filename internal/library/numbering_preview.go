package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// NumberingRequest selects the entries a numbering preview covers.
type NumberingRequest struct {
	Parent   int64
	Media    string // image, audio or mixed
	Query    string // name filter; empty for all
	Status   string // processing status filter; empty for all
	Sort     string
	Settings domain.NumberingSettings
}

// PreviewNumbering previews numbering of the media under req.Parent that match
// req.Query and req.Status, in req.Sort order.
func (l *Library) PreviewNumbering(ctx context.Context, req NumberingRequest) (Numbering, error) {
	parent, media, query, status, sortBy, settings := req.Parent, req.Media, req.Query, req.Status, req.Sort, req.Settings
	statusFilter, err := processingStatusFilter(status)
	if err != nil {
		return Numbering{}, err
	}
	if err := l.lockNumbering(ctx); err != nil {
		return Numbering{}, err
	}
	defer l.mu.Unlock()
	// Scan publication holds the same mutex. A queued scan or its pending flag cleanup
	// does not invalidate the complete index and disk checks below.
	m, err := mediaOf(media)
	if err != nil {
		return Numbering{}, err
	}
	if err = domain.ValidateNumberingSettings(settings); err != nil {
		return Numbering{}, err
	}
	rules, peerRules := settings.Image, settings.Audio
	peer := numberingMedia["audio"]
	if m.value == "audio" {
		rules, peerRules = settings.Audio, settings.Image
		peer = numberingMedia["image"]
	}
	if err = l.CheckAvailable(ctx, parent); err != nil {
		return Numbering{}, err
	}
	dir, err := l.Get(ctx, parent)
	if err != nil {
		return Numbering{}, err
	}
	if dir.Kind != "directory" {
		return Numbering{}, errNotDirectory
	}
	if l.Busy != nil && l.Busy(ctx, parent) {
		return Numbering{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonDescendantBusy, Message: "a descendant has an active job"})
	}
	path, err := l.Path(ctx, parent)
	if err != nil {
		return Numbering{}, err
	}
	before, err := directoryFingerprint(ctx, path)
	if err != nil {
		return Numbering{}, err
	}
	value, sortBy := directoryOrder(sortBy)
	order := "ASC"
	if strings.HasSuffix(sortBy, "_desc") {
		order = "DESC"
	}
	type candidate struct {
		id       int64
		selected bool
	}
	var candidates []candidate
	err = l.db.View(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT entry,instr(lower(name),lower(?))>0 AND (`+statusFilter+`) FROM directory_items WHERE dir=? AND generation=(SELECT generation FROM directory_state WHERE dir=?) AND kind='file' ORDER BY `+value+` `+order+`,name COLLATE NATURAL_NAME `+order+`,entry `+order, query, parent, parent)
		if err != nil {
			return err
		}
		candidates, err = storage.CollectRows(rows, func(rows *sql.Rows) (candidate, error) {
			var c candidate
			err := rows.Scan(&c.id, &c.selected)
			return c, err
		})
		return err
	})
	if err != nil {
		return Numbering{}, err
	}
	var selected, outside, peers []Entry
	indexed := map[string]bool{}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return Numbering{}, err
		}
		e, err := l.Get(ctx, c.id)
		if err != nil {
			return Numbering{}, err
		}
		sameClass := m.match(e.Name)
		if !sameClass && !(settings.Shared && peer.match(e.Name)) {
			continue
		}
		if sameClass {
			item := NumberingItem{Identity: e.Identity, Size: e.Size, ModifiedNS: e.ModifiedNS}
			if !numberingMatch(filepath.Join(path, e.Name), item) {
				return Numbering{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingMediaChanged, Message: "a file changed; refresh the directory", Details: map[string]any{"media": m.value}})
			}
		}
		indexed[e.Name] = true
		switch {
		case !sameClass:
			peers = append(peers, e)
		case c.selected && e.Processed != "marked":
			selected = append(selected, e)
		default:
			outside = append(outside, e)
		}
	}
	err = scanNumberingDirectory(ctx, path, func(e os.DirEntry) error {
		if !e.Type().IsRegular() || indexed[e.Name()] {
			return nil
		}
		if m.match(e.Name()) || (settings.Shared && peer.match(e.Name())) {
			return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingIndexIncomplete, Message: "the index is incomplete; refresh the directory", Details: map[string]any{"media": m.value}})
		}
		return nil
	})
	if err != nil {
		return Numbering{}, err
	}
	after, err := directoryFingerprint(ctx, path)
	if err != nil {
		return Numbering{}, err
	}
	if before != after {
		return Numbering{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonDirectoryChanged, Message: "directory changed; preview again"})
	}
	if len(selected) == 0 {
		return Numbering{}, domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonNumberingNothingSelected, Message: "no file to number in the current filter", Details: map[string]any{"media": m.value}})
	}
	items := numberingNames(numberingNamesOptions{Selected: selected, Outside: outside, Peers: peers, Rules: rules, PeerPrefix: peerRules.Prefix})
	if media == "mixed" {
		items = mixedNumberingNames(selected, outside, settings)
	}
	b := Numbering{ID: numberingToken(), Parent: parent, Created: storage.Now(), Status: "preview", Fingerprint: before, Media: m.value, Items: items}
	return l.saveNumberingPreview(ctx, b, path)
}

// Resolve an occupied target using only names returned by ReadDir. Looking up a
// future case alias (e.g. 5.jpeg for 5.JPEG) can populate a separate SMB dentry
// that survives moving the actual source to its temporary name.
func numberingTargetMoves(name string, names map[string][]string, changed map[string]NumberingItem) (NumberingItem, bool) {
	candidates := names[strings.ToLower(name)]
	if slices.Contains(candidates, name) {
		item, ok := changed[name]
		return item, ok
	}
	if len(candidates) != 1 || !strings.EqualFold(candidates[0], name) {
		return NumberingItem{}, false
	}
	item, ok := changed[candidates[0]]
	return item, ok
}

// numberingConflictOptions groups the inputs for numberingConflict.
type numberingConflictOptions struct {
	Path       string
	Batch      Numbering
	Item       NumberingItem
	Target     os.FileInfo
	Candidates []string
}

// Preview failures have no committed journal. Return evidence to the numbering
// dialog so a conflict can be inspected before any file is renamed.
func numberingConflict(params numberingConflictOptions) error {
	path, b, item, target, candidates := params.Path, params.Batch, params.Item, params.Target, params.Candidates

	var detail strings.Builder
	fmt.Fprintf(&detail, "numbering target conflict\ndirectory: %q\noriginal name: %q\ntarget name: %q\nmedia: %s\nfiles: %d; planned renames: %d\nindexed identity: %s\nsize: %d bytes\nmtime_ns: %d\n", path, item.Old, item.New, b.Media, len(b.Items), b.Changed, item.Identity, item.Size, item.ModifiedNS)
	fmt.Fprintf(&detail, "target kind: %s\ntarget size: %d bytes\n", kindOf(target), target.Size())
	if snap, err := domain.SnapshotFromInfo(target); err == nil {
		fmt.Fprintf(&detail, "target identity: %s\n", Identity(snap))
	}
	// Reuse the target and name candidates from the failed check. Only inspect
	// matching names, rather than statting every unrelated file for diagnostics.
	if len(candidates) == 0 {
		fmt.Fprintln(&detail, "the target path exists, but the directory listing has no exact or case-insensitive match; the two reads disagree and the cause is unknown.")
	}
	for _, name := range candidates {
		actual := target
		if name != item.New {
			var err error
			actual, err = os.Lstat(filepath.Join(path, name))
			if err != nil {
				fmt.Fprintf(&detail, "read case-insensitive match %q: %v\n", name, err)
				continue
			}
		}
		fmt.Fprintf(&detail, "same file as the target path: %t\n", os.SameFile(target, actual))
		if snapshot, err := domain.SnapshotFromInfo(actual); err == nil {
			fmt.Fprintf(&detail, "entry identity: %s; size: %d; mtime_ns: %d\n", Identity(snapshot), snapshot.Size, stamp(snapshot))
		}
		fmt.Fprintf(&detail, "occupying entry: %q (%s)\n", name, kindOf(actual))
		participation := "not part of this numbering"
		for _, planned := range b.Items {
			if planned.Old == name {
				participation = fmt.Sprintf("part of this numbering, planned %q -> %q", planned.Old, planned.New)
				break
			}
		}
		fmt.Fprintf(&detail, "occupying entry status: %s\n", participation)
	}
	fmt.Fprintln(&detail, "This preview changed no file. Entries seen as directories or symbolic links are skipped but their names can still occupy a target. Network shares can report file types that differ from the server.")
	return &domain.JobError{Code: domain.CodeRenameConflict, Message: fmt.Sprintf("target %s is occupied by an entry outside this numbering (source %s)", item.New, item.Old), Details: map[string]any{"reason": "target_occupied", "from": item.Old, "to": item.New}, ProcessingLog: detail.String()}
}

func (l *Library) saveNumberingPreview(ctx context.Context, b Numbering, path string) (Numbering, error) {
	changed := map[string]NumberingItem{}
	identities := map[string]bool{}
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return Numbering{}, err
		}
		if err := ValidName(i.New); err != nil {
			return Numbering{}, err
		}
		if identities[i.Identity] {
			return Numbering{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingHardLink, Message: "files with hard links cannot be numbered safely", Details: map[string]any{"media": b.Media}})
		}
		identities[i.Identity] = true
		if i.Old != i.New {
			changed[i.Old] = i
			b.Changed++
		}
	}
	names := map[string][]string{}
	err := scanNumberingDirectory(ctx, path, func(entry os.DirEntry) error {
		key := strings.ToLower(entry.Name())
		names[key] = append(names[key], entry.Name())
		return nil
	})
	if err != nil {
		return Numbering{}, err
	}
	for _, candidates := range names {
		if err := ctx.Err(); err != nil {
			return Numbering{}, err
		}
		slices.Sort(candidates)
	}
	for idx := range b.Items {
		if err := ctx.Err(); err != nil {
			return Numbering{}, err
		}
		i := &b.Items[idx]
		i.Current = i.Old
		i.Temp = ".video-rename-" + b.ID + fmt.Sprintf("-%d.tmp", idx)
		if _, err := os.Lstat(filepath.Join(path, i.Temp)); !errors.Is(err, fs.ErrNotExist) {
			return Numbering{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingTempConflict, Message: "temporary name conflict; preview again"})
		}
		if i.Old == i.New {
			continue
		}
		candidates := names[strings.ToLower(i.New)]
		if len(candidates) == 0 {
			// The final no-replace syscall still rejects any later occupant.
			continue
		}
		moving, moves := numberingTargetMoves(i.New, names, changed)
		actual := candidates[0]
		if slices.Contains(candidates, i.New) {
			actual = i.New
		}
		// Inspect the real entry, never the not-yet-created lowercase alias.
		target, err := os.Lstat(filepath.Join(path, actual))
		if err != nil {
			return Numbering{}, err
		}
		snapshot, err := domain.SnapshotFromInfo(target)
		if err != nil {
			return Numbering{}, err
		}
		if !moves || !target.Mode().IsRegular() || Identity(snapshot) != moving.Identity || snapshot.Size != moving.Size || stamp(snapshot) != moving.ModifiedNS {
			return Numbering{}, numberingConflict(numberingConflictOptions{Path: path, Batch: b, Item: *i, Target: target, Candidates: candidates})
		}
	}
	b.Total = len(b.Items)
	return storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (Numbering, error) {
		var err error
		if _, err = tx.ExecContext(ctx, "INSERT INTO image_numberings(id,parent,status,created,fingerprint,undo_of,media) VALUES(?,?,?,?,?,?,?)", b.ID, b.Parent, b.Status, b.Created, b.Fingerprint, b.UndoOf, b.Media); err != nil {
			return Numbering{}, err
		}
		for idx, i := range b.Items {
			if _, err = tx.ExecContext(ctx, `INSERT INTO image_numbering_items(batch,position,entry,version,identity,size,mtime_ns,old_name,new_name,temp_name,current_name,undo_of) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, b.ID, idx, i.Entry, i.Version, i.Identity, i.Size, i.ModifiedNS, i.Old, i.New, i.Temp, i.Current, i.UndoOf); err != nil {
				return Numbering{}, err
			}
		}
		// Unconfirmed previews are disposable; completed journals remain available for restore.
		if _, err = tx.ExecContext(ctx, `DELETE FROM image_numbering_items WHERE batch IN (SELECT id FROM image_numberings WHERE status='preview' AND created<?)`, b.Created[:10]); err != nil {
			return Numbering{}, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM image_numberings WHERE status='preview' AND created<?`, b.Created[:10]); err != nil {
			return Numbering{}, err
		}
		return b, nil
	})
}
