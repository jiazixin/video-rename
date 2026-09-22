package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/naming"
	"video-rename/internal/rename"
	"video-rename/internal/runlog"
)

// eventLog 是提交与运行生命周期实际使用的日志能力。
type eventLog interface {
	Pending(runlog.Event) error
	Success(runlog.Event) error
	Failure(runlog.Event) error
	RunID() string
	Dir() string
	Close() error
}

// committer 串行化名称分配、pending 日志、源核验、原子 rename 与最终事件。
// 所有改名和结果写入都在同一把锁内完成,保证本次任务一致性;
// 跨进程竞争由 no-replace 系统调用的原子性兜底。
type committer struct {
	mu           sync.Mutex
	log          eventLog
	alloc        *naming.Allocator
	dryRun       bool
	mode         string
	cfg          *config.Config
	stats2       counters
	logFailed    bool
	onLogFailure func(error, runlog.Event, string)
}

func newCommitter(w eventLog, dryRun bool, mode string, cfg *config.Config) *committer {
	return &committer{log: w, alloc: naming.NewAllocator(), dryRun: dryRun, mode: mode, cfg: cfg}
}

// counters 最终结果计数。
type counters struct {
	renamed, previewed, unchanged, failed, cancelled int
}

func (c *committer) stats() counters {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats2
}

// commit 处理一个任务的最终结果:成功提交、unchanged、预览、取消或失败。
func (c *committer) commit(ctx context.Context, job domain.VideoJob, rec *jobRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev := c.baseEvent(job, rec)
	// 仅在进入临界区前检查取消；pending 开始后保持 rename 与记账连贯。
	if ctx.Err() != nil || c.logFailed {
		rec.jobErr = &domain.JobError{Code: domain.CodeCancelled, Message: "提交前运行已停止"}
	}
	if c.logFailed {
		c.stats2.cancelled++
		return // 日志已损坏，统计仍完整，但不能声称已写入取消事件。
	}

	if rec.jobErr != nil {
		if rec.jobErr.Code == domain.CodeCancelled {
			ev.Status = string(domain.StatusCancelled)
			ev.Stage = string(rec.stage)
			ev.AbortReason = rec.jobErr.Message
			c.stats2.cancelled++
			if werr := c.log.Failure(ev); werr != nil {
				c.logFailLocked(werr, ev, "写取消事件失败")
			}
			return
		}
		c.failLocked(ev, rec, rec.jobErr)
		return
	}

	title, jerr := naming.CleanTitle(rec.title.Title, filepath.Ext(job.Path))
	if jerr != nil {
		ev.Stage = string(domain.StageNaming)
		c.failLocked(ev, rec, jerr)
		return
	}

	dir := filepath.Dir(job.Path)
	ext := filepath.Ext(job.Path) // 分配目标名称时统一为小写

	for {
		allocRes, aerr := c.alloc.Allocate(dir, title, ext, job)
		if aerr != nil {
			ev.Stage = string(domain.StageNaming)
			c.failLocked(ev, rec, aerr)
			return
		}
		ev.TargetPath = allocRes.Target

		if allocRes.Unchanged {
			ev.Status = string(domain.StatusUnchanged)
			ev.Stage = string(domain.StageNaming)
			c.stats2.unchanged++
			c.writeSuccessLocked(ev)
			return
		}

		if c.dryRun {
			// 预览:虚拟保留集合,不写 pending、不调用 rename
			ev.Status = string(domain.StatusPreviewed)
			ev.Stage = string(domain.StageNaming)
			c.stats2.previewed++
			c.writeSuccessLocked(ev)
			return
		}

		// 提交前重新核验源文件身份、大小与 mtime
		ev.Stage = string(domain.StageCommit)
		ev.Status = "pending"
		if werr := c.log.Pending(ev); werr != nil {
			c.stats2.failed++
			rec.jobErr = &domain.JobError{Code: domain.CodeLogWriteFailed, Message: "pending 写入失败，未执行改名"}
			c.logFailLocked(werr, ev, "写 rename_pending 失败,未执行改名")
			return
		}
		cur, serr := domain.StatSnapshot(job.Path)
		if serr != nil {
			c.failLocked(ev, rec, &domain.JobError{
				Code: domain.CodeSourceChanged, Message: fmt.Sprintf("提交前核验失败: %v", serr),
			})
			return
		}
		if !cur.Same(job.Snapshot) {
			c.failLocked(ev, rec, &domain.JobError{
				Code: domain.CodeSourceChanged, Message: "提交前核验发现源文件已被外部修改",
			})
			return
		}

		rerr := rename.NoReplace(job.Path, allocRes.Target)
		switch {
		case rerr == nil:
			ev.Status = string(domain.StatusRenamed)
			c.stats2.renamed++
			if werr := c.log.Success(ev); werr != nil {
				c.logFailLocked(werr, ev, "文件已改名但结果日志未确认")
			}
			return
		case rerr.Code == domain.CodeRenameConflict:
			// 目标被并发占用:释放候选,换下一后缀重试
			c.alloc.Release(dir, filepath.Base(allocRes.Target))
			continue
		default:
			c.failLocked(ev, rec, rerr)
			return
		}
	}
}

// cancelled 为未执行的任务记录取消事件。
func (c *committer) cancelled(job domain.VideoJob, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev := runlog.Event{
		JobID:       job.JobID,
		Mode:        c.mode,
		Status:      string(domain.StatusCancelled),
		SourcePath:  job.Path,
		Stage:       string(domain.StageDiscovery),
		AbortReason: reason,
	}
	c.stats2.cancelled++
	if c.logFailed {
		return
	}
	if werr := c.log.Failure(ev); werr != nil {
		c.logFailLocked(werr, ev, "写取消事件失败")
	}
}

// baseEvent 组装任务的公共事件字段(含脱敏:不写 prompt 原文与 API key)。
func (c *committer) baseEvent(job domain.VideoJob, rec *jobRecord) runlog.Event {
	warnings := append([]string(nil), rec.warnings...)
	if rec.frames.Failed != nil && len(rec.frames.Failed) > 0 {
		warnings = append(warnings, fmt.Sprintf("失败采样点 %d 个", len(rec.frames.Failed)))
	}
	ev := runlog.Event{
		JobID:          job.JobID,
		Mode:           c.mode,
		SourcePath:     job.Path,
		SourceSnapshot: &runlog.SourceSnapshot{Dev: job.Snapshot.Dev, Ino: job.Snapshot.Ino, Size: job.Snapshot.Size, ModTime: job.Snapshot.ModTime.UTC()},
		ElapsedMS:      elapsedMS(rec),
		Warnings:       warnings,
		Model:          c.cfg.Server.Model,
		PromptHash:     c.cfg.PromptHash(),
	}
	if rec.meta.Duration > 0 {
		ev.Metadata = runlog.Metadata{
			SizeBytes:       job.Snapshot.Size,
			DurationSeconds: rec.meta.Duration.Seconds(),
			Width:           rec.meta.DisplayWidth,
			Height:          rec.meta.DisplayHeight,
		}
	}
	ev.FramesPlanned = rec.frames.Plan.TargetN
	ev.FramesSent = rec.title.FramesSent
	ev.HTTPAttempts = rec.title.Attempts
	return ev
}

// writeSuccessLocked 记录成功最终事件。
func (c *committer) writeSuccessLocked(ev runlog.Event) {
	if werr := c.log.Success(ev); werr != nil {
		c.logFailLocked(werr, ev, "写成功事件失败")
	}
}

// failLocked 记录失败最终事件。
func (c *committer) failLocked(ev runlog.Event, rec *jobRecord, jerr *domain.JobError) {
	ev.Status = string(domain.StatusFailed)
	if ev.Stage == "" {
		ev.Stage = string(rec.stage)
	}
	ev.ErrorCode = string(jerr.Code)
	ev.ErrorMessage = jerr.Message
	ev.ExitCode = jerr.ExitCode
	ev.HTTPStatus = jerr.HTTPStatus
	ev.StderrTail = clampStderr(jerr.StderrTail)
	c.stats2.failed++
	if werr := c.log.Failure(ev); werr != nil {
		c.logFailLocked(werr, ev, "写失败事件失败")
	}
}

// logFailLocked 日志系统故障:标记停止提交(退出码 2 路径)。
func (c *committer) logFailLocked(werr error, ev runlog.Event, hint string) {
	if c.logFailed {
		return
	}
	c.logFailed = true
	if c.onLogFailure != nil {
		c.onLogFailure(werr, ev, hint)
	}
}

func elapsedMS(rec *jobRecord) int64 {
	return time.Since(rec.started).Milliseconds()
}

// clampStderr 限制 stderr 摘要长度。
func clampStderr(s string) string {
	const max = 8 * 1024
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(截断)"
}
