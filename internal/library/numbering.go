package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"video-rename/internal/domain"
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

// Saved file snapshots can explain a fingerprint mismatch without relaxing
// identity checks on network filesystems. Limit diagnostics for large batches.
func numberingFingerprintFailure(path string, b Numbering, actual string) error {
	var detail strings.Builder
	fmt.Fprintf(&detail, "编号目录校验诊断\n目录：%q\n预览时间：%s\n预览指纹：%s\n当前指纹：%s\n", path, b.Created, numberingFingerprintLabel(b.Fingerprint), numberingFingerprintLabel(actual))
	if !describeNumberingSnapshotDiff(&detail, b.Fingerprint, actual) {
		fmt.Fprintln(&detail, "预览格式无效，无法比较目录快照。当前目录尚未开始改名，请重新预览。")
		return &domain.JobError{Code: domain.CodeSourceChanged, Message: fmt.Sprintf("目录 %s 的预览格式无效，请重新预览", path), ProcessingLog: detail.String()}
	}
	fmt.Fprintln(&detail, "当前目录尚未开始改名。以上为两次读取的差异，不能据此确定变化原因；请核对具体条目后重新预览。")
	return &domain.JobError{Code: domain.CodeSourceChanged, Message: fmt.Sprintf("目录 %s 的预览校验不一致，请查看诊断详情并重新预览", path), ProcessingLog: detail.String()}
}

func (l *Library) checkNumberingAvailable(id int64) error {
	var pending bool
	if err := l.DB.Read().QueryRow("SELECT EXISTS(SELECT 1 FROM image_numberings WHERE status IN ('running','rollback','review'))").Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	var n int
	err := l.DB.Read().QueryRow(`WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1), ancestors(id,parent) AS (SELECT id,parent FROM entries WHERE id=? UNION ALL SELECT e.id,e.parent FROM entries e JOIN ancestors a ON e.id=a.parent)
 SELECT count(*) FROM image_numberings WHERE status IN ('running','rollback','review') AND (parent IN (SELECT id FROM descendants) OR parent IN (SELECT id FROM ancestors))`, id, id).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("该目录有未完成的编号，请先在编号记录中恢复")
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
	return err == nil && Identity(s) == i.Identity && s.Size == i.Size && stamp(s) == i.Mtime
}
