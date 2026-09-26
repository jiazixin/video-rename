package domain

import (
	"fmt"
	"strings"
	"time"
)

// SourceChangedError preserves the evidence without weakening source checks.
func SourceChangedError(message, stage, path string, before, after Snapshot) *JobError {
	var changed []string
	if before.Dev != after.Dev {
		changed = append(changed, "设备号")
	}
	if before.Ino != after.Ino {
		changed = append(changed, "inode")
	}
	if before.Size != after.Size {
		changed = append(changed, "大小")
	}
	if !before.ModTime.Equal(after.ModTime) {
		changed = append(changed, "修改时间")
	}
	return &JobError{Code: CodeSourceChanged, Message: message, ProcessingLog: fmt.Sprintf(
		"源文件校验诊断\n校验时间（UTC）：%s\n阶段：%s\n路径：%q\n差异字段：%s\n设备号：%d → %d\ninode：%d → %d\n大小（字节）：%d → %d\n修改时间（UTC，纳秒精度）：%s → %s\n说明：以上为文件系统元数据比较，不是内容哈希比较；差异不能单独证明视频内容被修改。\n",
		time.Now().UTC().Format(time.RFC3339Nano), stage, path, strings.Join(changed, "、"), before.Dev, after.Dev, before.Ino, after.Ino, before.Size, after.Size, before.ModTime.UTC().Format(time.RFC3339Nano), after.ModTime.UTC().Format(time.RFC3339Nano))}
}
