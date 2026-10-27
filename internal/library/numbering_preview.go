package library

import (
	"context"
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
		return Numbering{}, domain.Fail(domain.KindConflict, "descendant_busy", "a descendant has an active job")
	}
	path, err := l.Path(ctx, parent)
	if err != nil {
		return Numbering{}, err
	}
	before, err := directoryFingerprint(path)
	if err != nil {
		return Numbering{}, err
	}
	value, sortBy := directoryOrder(sortBy)
	order := "ASC"
	if strings.HasSuffix(sortBy, "_desc") {
		order = "DESC"
	}
	rows, err := l.DB.QueryContext(ctx, `SELECT entry,instr(lower(name),lower(?))>0 AND (`+statusFilter+`) FROM directory_items WHERE dir=? AND generation=(SELECT generation FROM directory_state WHERE dir=?) AND kind='file' ORDER BY `+value+` `+order+`,name COLLATE NATURAL_NAME `+order+`,entry `+order, query, parent, parent)
	if err != nil {
		return Numbering{}, err
	}
	type candidate struct {
		id       int64
		selected bool
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.selected); err != nil {
			rows.Close()
			return Numbering{}, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
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
			item := NumberingItem{Identity: e.Identity, Size: e.Size, Mtime: e.Mtime}
			if !numberingMatch(filepath.Join(path, e.Name), item) {
				return Numbering{}, domain.Fail(domain.KindConflict, "numbering_media_changed", "a file changed; refresh the directory", "media", m.value)
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
	disk, err := os.ReadDir(path)
	if err != nil {
		return Numbering{}, err
	}
	for _, e := range disk {
		if !e.Type().IsRegular() || indexed[e.Name()] {
			continue
		}
		if m.match(e.Name()) || (settings.Shared && peer.match(e.Name())) {
			return Numbering{}, domain.Fail(domain.KindConflict, "numbering_index_incomplete", "the index is incomplete; refresh the directory", "media", m.value)
		}
	}
	after, err := directoryFingerprint(path)
	if err != nil {
		return Numbering{}, err
	}
	if before != after {
		return Numbering{}, domain.Fail(domain.KindConflict, "directory_changed", "directory changed; preview again")
	}
	if len(selected) == 0 {
		return Numbering{}, domain.Fail(domain.KindInvalid, "numbering_nothing_selected", "no file to number in the current filter", "media", m.value)
	}
	items := numberingNames(selected, outside, peers, rules, peerRules.Prefix)
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

// Preview failures have no committed journal. Return evidence to the numbering
// dialog so a conflict can be inspected before any file is renamed.
func numberingConflict(path string, b Numbering, item NumberingItem, target os.FileInfo, candidates []string) error {
	var detail strings.Builder
	fmt.Fprintf(&detail, "numbering target conflict\ndirectory: %q\noriginal name: %q\ntarget name: %q\nmedia: %s\nfiles: %d; planned renames: %d\nindexed identity: %s\nsize: %d bytes\nmtime: %s\n", path, item.Old, item.New, b.Media, len(b.Items), b.Changed, item.Identity, item.Size, item.Mtime)
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
			fmt.Fprintf(&detail, "entry identity: %s; size: %d; mtime: %s\n", Identity(snapshot), snapshot.Size, stamp(snapshot))
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
		if err := ValidName(i.New); err != nil {
			return Numbering{}, err
		}
		if identities[i.Identity] {
			return Numbering{}, domain.Fail(domain.KindConflict, "numbering_hard_link", "files with hard links cannot be numbered safely", "media", b.Media)
		}
		identities[i.Identity] = true
		if i.Old != i.New {
			changed[i.Old] = i
			b.Changed++
		}
	}
	disk, err := os.ReadDir(path)
	if err != nil {
		return Numbering{}, err
	}
	names := make(map[string][]string, len(disk))
	for _, entry := range disk {
		key := strings.ToLower(entry.Name())
		names[key] = append(names[key], entry.Name())
	}
	for idx := range b.Items {
		if err := ctx.Err(); err != nil {
			return Numbering{}, err
		}
		i := &b.Items[idx]
		i.Current = i.Old
		i.Temp = ".video-rename-" + b.ID + fmt.Sprintf("-%d.tmp", idx)
		if _, err := os.Lstat(filepath.Join(path, i.Temp)); !errors.Is(err, fs.ErrNotExist) {
			return Numbering{}, domain.Fail(domain.KindConflict, "numbering_temp_conflict", "temporary name conflict; preview again")
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
		if !moves || !target.Mode().IsRegular() || Identity(snapshot) != moving.Identity || snapshot.Size != moving.Size || stamp(snapshot) != moving.Mtime {
			return Numbering{}, numberingConflict(path, b, *i, target, candidates)
		}
	}
	b.Total = len(b.Items)
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return Numbering{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO image_numberings(id,parent,status,created,fingerprint,undo_of,media) VALUES(?,?,?,?,?,?,?)", b.ID, b.Parent, b.Status, b.Created, b.Fingerprint, b.UndoOf, b.Media); err != nil {
		return Numbering{}, err
	}
	for idx, i := range b.Items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO image_numbering_items(batch,position,entry,version,identity,size,mtime,old_name,new_name,temp_name,current_name,undo_of) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, b.ID, idx, i.Entry, i.Version, i.Identity, i.Size, i.Mtime, i.Old, i.New, i.Temp, i.Current, i.UndoOf); err != nil {
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
	if err = tx.Commit(); err != nil {
		return Numbering{}, err
	}
	return b, nil
}
