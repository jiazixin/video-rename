package library

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-rename/internal/domain"
)

// Keep the error summary and its diagnostic together in the existing TEXT field.
// Plain error records remain readable; no database schema change is needed.
type numberingErrorRecord struct {
	Message       string `json:"message"`
	ProcessingLog string `json:"processing_log"`
}

func (b Numbering) numberingErrorRecord() string {
	if b.ProcessingLog == "" {
		return b.Error
	}
	data, err := json.Marshal(numberingErrorRecord{b.Error, b.ProcessingLog})
	if err != nil {
		return b.Error
	}
	return string(data)
}

func (b *Numbering) decodeNumberingError() {
	var record numberingErrorRecord
	if json.Unmarshal([]byte(b.Error), &record) == nil && record.ProcessingLog != "" {
		b.Error, b.ProcessingLog = record.Message, record.ProcessingLog
	}
}

// Capture after the failed syscall and before any rollback. These are subsequent
// observations, not an assertion about the filesystem at syscall time.
func numberingMoveFailure(b *Numbering, item *NumberingItem, path, target string, cause *domain.JobError) error {
	phase := "最终编号"
	if target == item.Temp {
		phase = "临时改名"
	}
	if b.Status == "rollback" {
		phase = "恢复原名"
	}
	var detail strings.Builder
	fmt.Fprintf(&detail, "编号执行失败诊断\n采集时间：%s\n批次：%s\n目录：%q\n阶段：%s\n原文件名：%q\n当前文件名：%q\n目标文件名：%q\n最终文件名：%q\n系统错误：%v\n预览身份：%s；大小：%d；修改时间：%s\n", time.Now().UTC().Format(time.RFC3339Nano), b.ID, path, phase, item.Old, item.Current, target, item.New, cause, item.Identity, item.Size, item.Mtime)
	inspect := func(label, name string) {
		info, err := os.Lstat(filepath.Join(path, name))
		if err != nil {
			fmt.Fprintf(&detail, "%s %q：%v\n", label, name, err)
			return
		}
		fmt.Fprintf(&detail, "%s %q：类型=%s；大小=%d；修改时间=%s", label, name, kindOf(info), info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
		if snapshot, err := domain.SnapshotFromInfo(info); err == nil {
			fmt.Fprintf(&detail, "；身份=%s", Identity(snapshot))
		}
		fmt.Fprintln(&detail)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(filepath.Join(path, name))
			fmt.Fprintf(&detail, "链接指向：%q；读取错误：%v\n", link, err)
		}
	}
	inspect("源路径", item.Current)
	inspect("目标路径", target)
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Fprintf(&detail, "读取目录失败：%v\n", err)
	} else {
		count := 0
		for _, entry := range entries {
			if !strings.EqualFold(entry.Name(), target) {
				continue
			}
			count++
			if count > 20 {
				continue
			}
			inspect("目标同名或大小写匹配条目", entry.Name())
			participation := "未参与本次编号"
			for _, planned := range b.Items {
				if entry.Name() == planned.Current || entry.Name() == planned.Old || entry.Name() == planned.New {
					participation = fmt.Sprintf("关联计划 %q → %q；记录当前位置 %q", planned.Old, planned.New, planned.Current)
					break
				}
			}
			fmt.Fprintln(&detail, participation)
		}
		fmt.Fprintf(&detail, "目标同名或大小写匹配条目总数：%d（最多展示 20 条）\n", count)
	}
	fmt.Fprintln(&detail, "以上为改名调用失败后、恢复操作前的读取结果；不能据此认定缓存异常或外部修改。")
	return &domain.JobError{Code: cause.Code, Message: fmt.Sprintf("%s失败：%q → %q：%s", phase, item.Current, target, cause.Message), ProcessingLog: detail.String()}
}
