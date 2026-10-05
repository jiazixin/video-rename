package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func (l *Library) PreviewNumberingContext(ctx context.Context, parent int64, media, query, sortBy string, settings domain.NumberingSettings) (Numbering, error) {
	return l.PreviewNumberingFilteredContext(ctx, parent, media, query, "", sortBy, settings)
}

func (l *Library) PreviewNumberingFilteredContext(ctx context.Context, parent int64, media, query, status, sortBy string, settings domain.NumberingSettings) (Numbering, error) {
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
	if err = l.CheckAvailable(parent); err != nil {
		return Numbering{}, err
	}
	dir, err := l.Get(parent)
	if err != nil {
		return Numbering{}, err
	}
	if dir.Kind != "directory" {
		return Numbering{}, fmt.Errorf("不是目录")
	}
	if l.Busy != nil && l.Busy(parent) {
		return Numbering{}, fmt.Errorf("目录有活动任务，请等待任务完成")
	}
	path, err := l.Path(parent)
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
		e, err := l.Get(c.id)
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
				return Numbering{}, fmt.Errorf("%s已变化，请刷新目录后重试", m.label)
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
			return Numbering{}, fmt.Errorf("%s索引不完整，请刷新目录后重试", m.label)
		}
	}
	after, err := directoryFingerprint(path)
	if err != nil {
		return Numbering{}, err
	}
	if before != after {
		return Numbering{}, fmt.Errorf("目录已变化，请重新预览")
	}
	if len(selected) == 0 {
		return Numbering{}, fmt.Errorf("当前筛选下没有可编号的%s", m.label)
	}
	items := numberingNames(selected, outside, peers, rules, peerRules.Prefix)
	if media == "mixed" {
		items = mixedNumberingNames(selected, outside, settings)
	}
	b := Numbering{ID: numberingToken(), Parent: parent, Created: storage.Now(), Status: "preview", Fingerprint: before, Media: m.value, Items: items}
	return l.saveNumberingPreviewContext(ctx, b, path)
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
	fmt.Fprintf(&detail, "编号目标冲突诊断\n目录：%q\n原文件名：%q\n目标文件名：%q\n媒体类型：%s\n参与文件：%d；计划改名：%d\n原文件索引身份：%s\n原文件大小：%d 字节\n原文件修改时间：%s\n", path, item.Old, item.New, b.Media, len(b.Items), b.Changed, item.Identity, item.Size, item.Mtime)
	fmt.Fprintf(&detail, "目标类型：%s\n目标大小：%d 字节\n", kindOf(target), target.Size())
	if snap, err := domain.SnapshotFromInfo(target); err == nil {
		fmt.Fprintf(&detail, "目标身份：%s\n", Identity(snap))
	}
	// Reuse the target and name candidates from the failed check. Only inspect
	// matching names, rather than statting every unrelated file for diagnostics.
	if len(candidates) == 0 {
		fmt.Fprintln(&detail, "目标路径查询成功，但目录列表中没有同名或大小写匹配项；两次读取结果不一致，尚不能确定原因。")
	}
	for _, name := range candidates {
		actual := target
		if name != item.New {
			var err error
			actual, err = os.Lstat(filepath.Join(path, name))
			if err != nil {
				fmt.Fprintf(&detail, "读取大小写匹配项 %q 失败：%v\n", name, err)
				continue
			}
		}
		fmt.Fprintf(&detail, "与目标路径身份相同：%t\n", os.SameFile(target, actual))
		if snapshot, err := domain.SnapshotFromInfo(actual); err == nil {
			fmt.Fprintf(&detail, "实际条目身份：%s；大小：%d；修改时间：%s\n", Identity(snapshot), snapshot.Size, stamp(snapshot))
		}
		fmt.Fprintf(&detail, "占用条目实际名称：%q（%s）\n", name, kindOf(actual))
		participation := "未参与本次编号"
		for _, planned := range b.Items {
			if planned.Old == name {
				participation = fmt.Sprintf("参与本次编号，计划 %q → %q", planned.Old, planned.New)
				break
			}
		}
		fmt.Fprintf(&detail, "占用条目状态：%s\n", participation)
	}
	fmt.Fprintln(&detail, "此次预览未修改文件；程序跳过识别为目录或符号链接的条目，其名称仍可能占用编号目标。网络共享返回的文件类型可能与服务端实际类型不同。")
	return &domain.JobError{Code: domain.CodeRenameConflict, Message: fmt.Sprintf("目标 %s 被未参与本次改名的文件或目录占用（原文件：%s）", item.New, item.Old), ProcessingLog: detail.String()}
}

func (l *Library) saveNumberingPreview(b Numbering, path string) (Numbering, error) {
	return l.saveNumberingPreviewContext(context.Background(), b, path)
}

func (l *Library) saveNumberingPreviewContext(ctx context.Context, b Numbering, path string) (Numbering, error) {
	label := mediaLabel(b.Media)
	changed := map[string]NumberingItem{}
	identities := map[string]bool{}
	for _, i := range b.Items {
		if err := ValidName(i.New); err != nil {
			return Numbering{}, err
		}
		if identities[i.Identity] {
			return Numbering{}, fmt.Errorf("%s包含硬链接，无法安全整批编号", label)
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
		if _, err := os.Lstat(filepath.Join(path, i.Temp)); !os.IsNotExist(err) {
			return Numbering{}, fmt.Errorf("临时名称冲突，请重新预览")
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
