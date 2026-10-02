package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/rename"
	"video-rename/internal/storage"
)

type NumberingItem struct {
	Entry     int64  `json:"entry"`
	Old       string `json:"old_name"`
	New       string `json:"new_name"`
	Version   int64  `json:"-"`
	Identity  string `json:"-"`
	Size      int64  `json:"-"`
	Mtime     string `json:"-"`
	Temp      string `json:"-"`
	Current   string `json:"-"`
	Pending   string `json:"-"`
	Operation int64  `json:"-"`
	UndoOf    int64  `json:"-"`
}
type Numbering struct {
	ID            string          `json:"id"`
	Parent        int64           `json:"parent"`
	Status        string          `json:"status"`
	Created       string          `json:"created"`
	UndoOf        string          `json:"undo_of"`
	Error         string          `json:"error"`
	ProcessingLog string          `json:"processing_log"`
	Total         int             `json:"total"`
	Changed       int             `json:"changed"`
	Items         []NumberingItem `json:"items,omitempty"`
	Fingerprint   string          `json:"-"`
	Media         string          `json:"-"`
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
	label string
	match func(name string) bool
}

var numberingMedia = map[string]mediaClass{
	"image": {"image", "图片", IsImage},
	"audio": {"audio", "音频", IsAudio},
	"mixed": {"mixed", "图片和音频", func(name string) bool { return IsImage(name) || IsAudio(name) }},
}

func mediaOf(value string) (mediaClass, error) {
	m, ok := numberingMedia[value]
	if !ok {
		return mediaClass{}, fmt.Errorf("无效的媒体类型")
	}
	return m, nil
}
func mediaLabel(value string) string {
	if m, ok := numberingMedia[value]; ok {
		return m.label
	}
	return "文件"
}

// numberedValue reads the ordinal of a name shaped prefix+digits; names with
// another shape carry no number and join the tail of the sequence.
func numberedValue(name, prefix string) *big.Int {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	base, found := strings.CutPrefix(base, prefix)
	if !found {
		return nil
	}
	if base == "" {
		return nil
	}
	for _, c := range base {
		if c < '0' || c > '9' {
			return nil
		}
	}
	n, ok := new(big.Int).SetString(base, 10)
	if !ok || n.Sign() <= 0 {
		return nil
	}
	return n
}

func padNumber(n *big.Int, width int) string {
	s := n.String()
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// Every format shares one sequence. Unselected files of the same class, and
// the peer class when one sequence is shared, reserve their numbers too.
func numberingNames(selected, outside, peers []Entry, r domain.MediaNumberingRules, peerPrefix string) []NumberingItem {
	max := new(big.Int)
	used := map[string]bool{}
	// Selected files raise the ceiling but never block their own number;
	// outside and peer files reserve theirs.
	scan := func(e Entry, prefix string, reserve bool) {
		if n := numberedValue(e.Name, prefix); n != nil {
			if n.Cmp(max) > 0 {
				max.Set(n)
			}
			if reserve {
				used[n.String()] = true
			}
		}
	}
	for _, e := range selected {
		scan(e, r.Prefix, false)
	}
	for _, e := range outside {
		scan(e, r.Prefix, true)
	}
	for _, e := range peers {
		scan(e, peerPrefix, true)
	}
	// Compact mode assigns fresh consecutive numbers and only steps around
	// numbers kept by files outside this batch.
	next := big.NewInt(0)
	result := make([]NumberingItem, 0, len(selected))
	for _, e := range selected {
		n := numberedValue(e.Name, r.Prefix)
		if r.Compact || n == nil || used[n.String()] {
			if r.Compact {
				next.Add(next, big.NewInt(1))
				for used[next.String()] {
					next.Add(next, big.NewInt(1))
				}
				n = new(big.Int).Set(next)
			} else {
				max.Add(max, big.NewInt(1))
				n = new(big.Int).Set(max)
			}
		}
		used[n.String()] = true
		ext := filepath.Ext(e.Name)
		if r.Lowercase {
			ext = strings.ToLower(ext)
		}
		result = append(result, NumberingItem{Entry: e.ID, Old: e.Name, New: r.Prefix + padNumber(n, r.Padding) + ext, Version: e.Version, Identity: e.Identity, Size: e.Size, Mtime: e.Mtime})
	}
	return result
}

// Saved file snapshots can explain a fingerprint mismatch without relaxing
// identity checks on network filesystems. Limit diagnostics for large batches.
func numberingFingerprintFailure(path string, b Numbering, actual string) error {
	var detail strings.Builder
	fmt.Fprintf(&detail, "编号目录校验诊断\n目录：%q\n预览时间：%s\n预览指纹：%s\n当前指纹：%s\n", path, b.Created, numberingFingerprintLabel(b.Fingerprint), numberingFingerprintLabel(actual))
	if !describeNumberingSnapshotDiff(&detail, b.Fingerprint, actual) {
		fmt.Fprintln(&detail, "此预览没有完整目录快照，以下仅比较参与编号的文件；更新后请重新预览。")
		differences := 0
		for _, item := range b.Items {
			info, err := os.Lstat(filepath.Join(path, item.Old))
			if err != nil {
				fmt.Fprintf(&detail, "文件 %q：读取失败：%v\n", item.Old, err)
				differences++
			} else {
				snap, err := domain.SnapshotFromInfo(info)
				if err != nil {
					fmt.Fprintf(&detail, "文件 %q：身份读取失败：%v\n", item.Old, err)
					differences++
				} else if !info.Mode().IsRegular() || Identity(snap) != item.Identity || snap.Size != item.Size || stamp(snap) != item.Mtime {
					fmt.Fprintf(&detail, "文件 %q：类型=%s；身份 %s → %s；大小 %d → %d；修改时间 %s → %s\n", item.Old, info.Mode().Type(), item.Identity, Identity(snap), item.Size, snap.Size, item.Mtime, stamp(snap))
					differences++
				}
			}
			if differences >= 20 {
				fmt.Fprintln(&detail, "差异仅展示前 20 项。")
				break
			}
		}
		if differences == 0 {
			fmt.Fprintln(&detail, "参与文件的身份、大小和修改时间一致；差异可能来自其他目录条目或旧版预览指纹，当前记录不足以确定具体条目。")
		}
	}
	fmt.Fprintln(&detail, "当前目录尚未开始改名。以上为两次读取的差异，不能据此确定变化原因；请核对具体条目后重新预览。")
	return &domain.JobError{Code: domain.CodeSourceChanged, Message: fmt.Sprintf("目录 %s 的预览校验不一致，请查看诊断详情并重新预览", path), ProcessingLog: detail.String()}
}

func (l *Library) checkNumberingAvailable(id int64) error {
	var n int
	err := l.DB.QueryRow(`WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1), ancestors(id,parent) AS (SELECT id,parent FROM entries WHERE id=? UNION ALL SELECT e.id,e.parent FROM entries e JOIN ancestors a ON e.id=a.parent)
 SELECT count(*) FROM image_numberings WHERE status IN ('running','rollback','review') AND (parent IN (SELECT id FROM descendants) OR parent IN (SELECT id FROM ancestors))`, id, id).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("该目录有未完成的编号，请先在编号记录中恢复")
	}
	return nil
}

func (l *Library) PreviewNumbering(parent int64, media, query, sortBy string, settings domain.NumberingSettings) (Numbering, error) {
	return l.PreviewNumberingContext(context.Background(), parent, media, query, sortBy, settings)
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

func (l *Library) PreviewNumberingContext(ctx context.Context, parent int64, media, query, sortBy string, settings domain.NumberingSettings) (Numbering, error) {
	if err := l.lockNumbering(ctx); err != nil {
		return Numbering{}, err
	}
	defer l.mu.Unlock()
	// Refresh holds the same mutex. A queued scan or its pending flag cleanup
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
	rows, err := l.DB.QueryContext(ctx, `SELECT entry,instr(lower(name),lower(?))>0 FROM directory_items WHERE dir=? AND generation=(SELECT generation FROM directory_state WHERE dir=?) AND kind='file' ORDER BY `+value+` `+order+`,name COLLATE NATURAL_NAME `+order+`,entry `+order, query, parent, parent)
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

// Plan both classes together so they share one fingerprint and rollback journal.
func mixedNumberingNames(entries, outside []Entry, settings domain.NumberingSettings) []NumberingItem {
	var images, audio, keptImages, keptAudio []Entry
	for _, e := range entries {
		if IsImage(e.Name) {
			images = append(images, e)
		} else {
			audio = append(audio, e)
		}
	}
	for _, e := range outside {
		if IsImage(e.Name) {
			keptImages = append(keptImages, e)
		} else {
			keptAudio = append(keptAudio, e)
		}
	}
	var peers []Entry
	if settings.Shared {
		peers = append(peers, keptAudio...)
		if !settings.Audio.Compact {
			peers = append(peers, audio...)
		}
	}
	items := numberingNames(images, keptImages, peers, settings.Image, settings.Audio.Prefix)
	peers = nil
	if settings.Shared {
		peers = append(peers, keptImages...)
		for _, item := range items {
			peers = append(peers, Entry{Name: item.New})
		}
	}
	return append(items, numberingNames(audio, keptAudio, peers, settings.Audio, settings.Image.Prefix)...)
}

func numberingMatch(path string, i NumberingItem) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	s, err := domain.SnapshotFromInfo(info)
	return err == nil && Identity(s) == i.Identity && s.Size == i.Size && stamp(s) == i.Mtime
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

func (l *Library) Numbering(id string) (Numbering, error) {
	var b Numbering
	err := l.DB.QueryRow("SELECT id,parent,status,created,fingerprint,undo_of,error,media FROM image_numberings WHERE id=?", id).Scan(&b.ID, &b.Parent, &b.Status, &b.Created, &b.Fingerprint, &b.UndoOf, &b.Error, &b.Media)
	if err != nil {
		return b, err
	}
	b.decodeNumberingError()
	rows, err := l.DB.Query(`SELECT entry,version,identity,size,mtime,old_name,new_name,temp_name,current_name,pending_name,operation,undo_of FROM image_numbering_items WHERE batch=? ORDER BY position`, id)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var i NumberingItem
		if err = rows.Scan(&i.Entry, &i.Version, &i.Identity, &i.Size, &i.Mtime, &i.Old, &i.New, &i.Temp, &i.Current, &i.Pending, &i.Operation, &i.UndoOf); err != nil {
			return b, err
		}
		b.Items = append(b.Items, i)
		if i.Old != i.New {
			b.Changed++
		}
	}
	b.Total = len(b.Items)
	return b, rows.Err()
}
func (l *Library) NumberingHistory(parent int64, media string) ([]Numbering, error) {
	if _, err := mediaOf(media); err != nil {
		return nil, err
	}
	rows, err := l.DB.Query(`SELECT b.id,b.parent,b.status,b.created,b.undo_of,b.error,count(i.entry),COALESCE(sum(i.old_name<>i.new_name),0) FROM image_numberings b JOIN image_numbering_items i ON i.batch=b.id WHERE b.parent=? AND b.media=? AND b.status<>'preview' GROUP BY b.id ORDER BY b.created DESC,b.id DESC LIMIT 50`, parent, media)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Numbering{}
	for rows.Next() {
		var b Numbering
		if err = rows.Scan(&b.ID, &b.Parent, &b.Status, &b.Created, &b.UndoOf, &b.Error, &b.Total, &b.Changed); err != nil {
			return nil, err
		}
		b.decodeNumberingError()
		result = append(result, b)
	}
	return result, rows.Err()
}
func (l *Library) PreviewNumberingRestore(id string) (Numbering, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	original, err := l.Numbering(id)
	if err != nil {
		return Numbering{}, err
	}
	if original.Status != "committed" || original.UndoOf != "" {
		return Numbering{}, fmt.Errorf("该批次不可还原")
	}
	if err = l.CheckAvailable(original.Parent); err != nil {
		return Numbering{}, err
	}
	path, err := l.Path(original.Parent)
	if err != nil {
		return Numbering{}, err
	}
	fingerprint, err := directoryFingerprint(path)
	if err != nil {
		return Numbering{}, err
	}
	b := Numbering{ID: numberingToken(), Parent: original.Parent, Created: storage.Now(), Status: "preview", Fingerprint: fingerprint, UndoOf: id, Media: original.Media}
	for _, i := range original.Items {
		if i.Old == i.New {
			continue
		}
		e, err := l.Get(i.Entry)
		if err != nil {
			return Numbering{}, err
		}
		if e.Parent != b.Parent || e.Name != i.New || e.RestoreID != i.Operation || !numberingMatch(filepath.Join(path, e.Name), i) {
			return Numbering{}, fmt.Errorf("%s %s 已变化或有后续改名，不能整批还原", mediaLabel(b.Media), i.New)
		}
		b.Items = append(b.Items, NumberingItem{Entry: e.ID, Version: e.Version, Identity: i.Identity, Size: i.Size, Mtime: i.Mtime, Old: i.New, New: i.Old, UndoOf: i.Operation})
	}
	if len(b.Items) == 0 {
		return Numbering{}, fmt.Errorf("该批次没有需要还原的%s", mediaLabel(b.Media))
	}
	return l.saveNumberingPreview(b, path)
}

func (l *Library) ApplyNumbering(id string) (Numbering, error) {
	return l.ApplyNumberingContext(context.Background(), id, nil)
}

func (l *Library) ApplyNumberingContext(ctx context.Context, id string, progress func(int, int)) (Numbering, error) {
	if err := l.lockNumbering(ctx); err != nil {
		return Numbering{}, err
	}
	defer l.mu.Unlock()
	b, err := l.Numbering(id)
	if err != nil {
		return b, err
	}
	if b.Status == "committed" || b.Status == "restored" {
		return b, nil
	}
	if b.Status != "preview" {
		return b, fmt.Errorf("该批次%s，请查看编号记录", b.Status)
	}
	if l.broken != nil {
		return b, fmt.Errorf("服务有待恢复操作，请重启核对")
	}
	if err = l.CheckAvailable(b.Parent); err != nil {
		return b, err
	}
	if l.Busy != nil && l.Busy(b.Parent) {
		return b, fmt.Errorf("目录有活动任务，请等待任务完成")
	}
	path, err := l.Path(b.Parent)
	if err != nil {
		return b, err
	}
	fingerprint, err := directoryFingerprint(path)
	if err != nil {
		return b, err
	}
	if fingerprint != b.Fingerprint {
		return b, numberingFingerprintFailure(path, b, fingerprint)
	}
	if b.UndoOf != "" {
		original, err := l.Numbering(b.UndoOf)
		if err != nil {
			return b, err
		}
		if original.Status != "committed" {
			return b, fmt.Errorf("原批次已还原或不可用")
		}
	}
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		e, err := l.Get(i.Entry)
		if err != nil {
			return b, err
		}
		if b.UndoOf == "" && e.Processed == "marked" {
			return b, fmt.Errorf("%s %s 已标记为跳过编号，请重新预览", mediaLabel(b.Media), e.Name)
		}
		if e.Parent != b.Parent || e.Version != i.Version || e.Name != i.Old || !numberingMatch(filepath.Join(path, i.Old), i) {
			return b, fmt.Errorf("%s %s 已变化，请重新预览", mediaLabel(b.Media), i.Old)
		}
		if err = l.CheckAvailable(i.Entry); err != nil {
			return b, err
		}
		if e.TaskStatus != "" || (l.Busy != nil && l.Busy(i.Entry)) {
			return b, fmt.Errorf("%s有活动任务，请等待完成", mediaLabel(b.Media))
		}
		if i.UndoOf != 0 && e.RestoreID != i.UndoOf {
			return b, fmt.Errorf("%s有后续改名，不能整批还原", mediaLabel(b.Media))
		}
	}
	if _, err = l.DB.Exec("UPDATE image_numberings SET status='running' WHERE id=?", id); err != nil {
		return b, err
	}
	b.Status = "running"
	if err = l.performNumbering(ctx, &b, path, progress); err != nil {
		// A failed COMMIT can have an uncertain outcome. Inspect durable state
		// before moving files back; if the database is unavailable, let startup recover.
		persisted, readErr := l.Numbering(id)
		if readErr != nil {
			l.broken = readErr
			return b, fmt.Errorf("编号结果待核对，请重启恢复：%w", readErr)
		}
		if persisted.Status == "committed" {
			return persisted, nil
		}
		b = persisted
		cause := err
		if diagnostic, ok := errors.AsType[*domain.JobError](cause); ok {
			b.ProcessingLog = diagnostic.ProcessingLog
		}
		b.Error = cause.Error()
		// Save the captured failure before rollback changes the filesystem.
		if _, saveErr := l.DB.Exec("UPDATE image_numberings SET error=? WHERE id=?", b.numberingErrorRecord(), id); saveErr != nil {
			l.broken = saveErr
			return b, fmt.Errorf("保存编号失败现场失败，需重启核对：%w", saveErr)
		}
		if recovery := l.rollbackNumbering(&b, path); recovery != nil {
			b.Status = "review"
			b.Error = fmt.Sprintf("%v；恢复未完成：%v", cause, recovery)
			if diagnostic, ok := errors.AsType[*domain.JobError](recovery); ok {
				b.ProcessingLog += "\n恢复失败现场：\n" + diagnostic.ProcessingLog
			}
		} else {
			b.Status = "rolled_back"
			b.Error = fmt.Sprintf("操作失败，已恢复原名：%v", cause)
		}
		if _, dbErr := l.DB.Exec("UPDATE image_numberings SET status=?,error=? WHERE id=?", b.Status, b.numberingErrorRecord(), id); dbErr != nil {
			l.broken = dbErr
		}
		if diagnostic, ok := errors.AsType[*domain.JobError](cause); ok {
			return b, &domain.JobError{Code: diagnostic.Code, Message: b.Error, ProcessingLog: b.ProcessingLog}
		}
		return b, errors.New(b.Error)
	}
	return l.Numbering(id)
}

// Persist the intent before each no-replace move, then fsync before recording completion.
func (l *Library) moveNumbering(b *Numbering, i *NumberingItem, path, target string) error {
	if i.Current == target {
		return nil
	}
	if checked, err := l.Path(b.Parent); err != nil || checked != path {
		return fmt.Errorf("父目录已变化")
	}
	dir, err := l.openDirectory(b.Parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	snap, err := directorySnapshot(dir, i.Current)
	if err != nil || Identity(snap) != i.Identity || snap.Size != i.Size || stamp(snap) != i.Mtime {
		return fmt.Errorf("%s %s 身份或内容已变化", mediaLabel(b.Media), i.Current)
	}
	if _, err := l.DB.Exec("UPDATE image_numbering_items SET pending_name=? WHERE batch=? AND entry=?", target, b.ID, i.Entry); err != nil {
		return err
	}
	i.Pending = target
	if err := rename.NoReplaceAt(int(dir.Fd()), i.Current, target); err != nil {
		return numberingMoveFailure(b, i, path, target, err)
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	if _, err := l.Path(b.Parent); err != nil {
		return err
	}
	if _, err := l.DB.Exec("UPDATE image_numbering_items SET current_name=?,pending_name='' WHERE batch=? AND entry=?", target, b.ID, i.Entry); err != nil {
		return err
	}
	i.Current = target
	i.Pending = ""
	return nil
}
func syncNumberingDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (l *Library) performNumbering(ctx context.Context, b *Numbering, path string, progress func(int, int)) error {
	done := 0
	for idx := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		i := &b.Items[idx]
		if i.Old != i.New {
			if err := l.moveNumbering(b, i, path, i.Temp); err != nil {
				return err
			}
		}
	}
	for idx := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		i := &b.Items[idx]
		if i.Old != i.New {
			if err := l.moveNumbering(b, i, path, i.New); err != nil {
				return err
			}
			done++
			if progress != nil {
				progress(done, b.Changed)
			}
		}
	}
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !numberingMatch(filepath.Join(path, i.New), i) {
			return fmt.Errorf("%s %s 在提交前发生变化", mediaLabel(b.Media), i.New)
		}
	}
	tx, err := l.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Temporarily free database name uniqueness within the same transaction.
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i.Old != i.New {
			if _, err = tx.Exec("UPDATE entries SET name=? WHERE id=?", i.Temp, i.Entry); err != nil {
				return err
			}
		}
	}
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i.Old == i.New {
			continue
		}
		source := "manual"
		if b.UndoOf != "" {
			source = "restore"
		}
		o := Operation{Entry: i.Entry, Parent: b.Parent, Old: i.Old, New: i.New, Identity: i.Identity, Size: i.Size, Mtime: i.Mtime, Kind: "file", Source: source, UndoOf: i.UndoOf}
		res, err := tx.Exec(`INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime,kind,source,status,undo_of,request_key,created) VALUES(?,?,?,?,?,?,?,?,?,'prepared',?,?,?)`, o.Entry, o.Parent, o.Old, o.New, o.Identity, o.Size, o.Mtime, o.Kind, o.Source, o.UndoOf, "numbering:"+b.ID+fmt.Sprint(i.Entry), storage.Now())
		if err != nil {
			return err
		}
		o.ID, _ = res.LastInsertId()
		oldrel, _ := filepath.Rel(l.Root, filepath.Join(path, i.Old))
		newrel, _ := filepath.Rel(l.Root, filepath.Join(path, i.New))
		if err = l.finishTx(tx, o, oldrel, newrel); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE image_numbering_items SET operation=? WHERE batch=? AND entry=?", o.ID, b.ID, i.Entry); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE image_numberings SET status='committed',error='' WHERE id=?", b.ID); err != nil {
		return err
	}
	if b.UndoOf != "" {
		if _, err = tx.Exec("UPDATE image_numberings SET status='restored' WHERE id=?", b.UndoOf); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

// Resolve a crash between filesystem rename and journal completion without guessing.
func (l *Library) reconcileNumbering(b *Numbering, path string) error {
	names, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	exact := map[string]bool{}
	for _, n := range names {
		exact[n.Name()] = true
	}
	for idx := range b.Items {
		i := &b.Items[idx]
		current := exact[i.Current] && numberingMatch(filepath.Join(path, i.Current), *i)
		if i.Pending != "" {
			pending := exact[i.Pending] && numberingMatch(filepath.Join(path, i.Pending), *i)
			if current == pending {
				return fmt.Errorf("无法唯一确定%s %s 的位置，需要人工核对", mediaLabel(b.Media), i.Old)
			}
			if pending {
				i.Current = i.Pending
			}
			if _, err = l.DB.Exec("UPDATE image_numbering_items SET current_name=?,pending_name='' WHERE batch=? AND entry=?", i.Current, b.ID, i.Entry); err != nil {
				return err
			}
			i.Pending = ""
		} else if !current {
			return fmt.Errorf("%s %s 已被外部修改，需要人工核对", mediaLabel(b.Media), i.Old)
		}
	}
	return syncNumberingDir(path)
}
func (l *Library) rollbackNumbering(b *Numbering, path string) error {
	if _, err := l.DB.Exec("UPDATE image_numberings SET status='rollback' WHERE id=?", b.ID); err != nil {
		return err
	}
	b.Status = "rollback"
	if err := l.reconcileNumbering(b, path); err != nil {
		return err
	}
	// Stage first, including final names that may occupy another item's original name.
	for idx := range b.Items {
		i := &b.Items[idx]
		if i.Current != i.Old {
			if err := l.moveNumbering(b, i, path, i.Temp); err != nil {
				return err
			}
		}
	}
	for idx := range b.Items {
		i := &b.Items[idx]
		if i.Current != i.Old {
			if err := l.moveNumbering(b, i, path, i.Old); err != nil {
				return err
			}
		}
	}
	return nil
}
func (l *Library) recoverNumberings() error {
	rows, err := l.DB.Query("SELECT id FROM image_numberings WHERE status IN ('running','rollback','review') ORDER BY created")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = l.recoverNumbering(id); err != nil {
			return err
		}
	}
	return nil
}
func (l *Library) RecoverNumbering(id string) (Numbering, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recoverNumbering(id)
}
func (l *Library) recoverNumbering(id string) (Numbering, error) {
	b, err := l.Numbering(id)
	if err != nil {
		return b, err
	}
	if b.Status != "running" && b.Status != "rollback" && b.Status != "review" {
		return b, nil
	}
	path, err := l.Path(b.Parent)
	if err == nil {
		err = l.rollbackNumbering(&b, path)
	}
	b.Status = "rolled_back"
	b.Error = fmt.Sprintf("中断的%s编号已恢复原名", mediaLabel(b.Media))
	if err != nil {
		b.Status = "review"
		b.Error = fmt.Sprintf("%s编号恢复未完成：%v", mediaLabel(b.Media), err)
		if diagnostic, ok := errors.AsType[*domain.JobError](err); ok {
			b.ProcessingLog += "\n恢复失败现场：\n" + diagnostic.ProcessingLog
		}
	}
	_, dbErr := l.DB.Exec("UPDATE image_numberings SET status=?,error=? WHERE id=?", b.Status, b.numberingErrorRecord(), id)
	return b, dbErr
}
