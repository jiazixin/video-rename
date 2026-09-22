package naming

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"video-rename/internal/domain"
)

// Allocator 串行分配同目录候选目标名称。
// 维护本次运行已保留的名称集合(大小写不敏感,保守处理大小写不敏感卷),
// 并结合磁盘现状与源文件身份判定 unchanged / 冲突后缀。
// 并发安全;由提交协调器串行调用。
type Allocator struct {
	mu       sync.Mutex
	reserved map[string]map[string]struct{} // dir → 小写名称集合
}

func NewAllocator() *Allocator {
	return &Allocator{reserved: map[string]map[string]struct{}{}}
}

// AllocateResult 候选分配结果。
type AllocateResult struct {
	Target    string // 目标绝对路径
	Unchanged bool   // 目标与源为同一文件且源扩展名已为小写
}

// Allocate 为清洗后的标题分配目标名称。
// 冲突时在扩展名前追加 " (2)"、" (3)"…,每次为后缀和扩展名重新预留字节;
// 名称空间超过 (9999) 报 RENAME_CONFLICT_EXHAUSTED。
// 候选指向源文件身份时，除需规范扩展名大小写外返回 Unchanged。
func (a *Allocator) Allocate(dir, title, ext string, job domain.VideoJob) (AllocateResult, *domain.JobError) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ext = strings.ToLower(ext)

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

		// 候选存在：同一身份仅在无需转换扩展名时跳过；不同身份尝试下一后缀。
		if snap, err := domain.StatSnapshot(target); err == nil {
			if snap.SameIdentity(job.Snapshot) {
				return AllocateResult{Target: target, Unchanged: filepath.Ext(job.Path) == ext}, nil
			}
			continue
		} else if !os.IsNotExist(err) {
			return AllocateResult{}, &domain.JobError{
				Code:    domain.CodeRenameFailed,
				Message: fmt.Sprintf("检查目标 %s 失败: %v", target, err),
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
		Message: fmt.Sprintf("目录 %s 中名称空间超过 (9999)", dir),
	}
}

// Release 归还保留的名称(目标竞争需要换后缀时调用方使用)。
func (a *Allocator) Release(dir, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if m := a.reserved[dir]; m != nil {
		delete(m, strings.ToLower(name))
	}
}
