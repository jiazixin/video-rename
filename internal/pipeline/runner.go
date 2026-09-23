// Package pipeline 串联发现、探测、抽帧、推理、命名与提交:
// 固定数量 worker、有界队列、串行提交协调器、两路日志与结果汇总。
package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/discovery"
	"video-rename/internal/domain"
	"video-rename/internal/media"
	"video-rename/internal/runlog"
	"video-rename/internal/vision"
)

// Options 运行选项;工具路径由调用方(main)在启动时解析。
type Options struct {
	Config        *config.Config
	Path          string
	Recursive     bool
	DryRun        bool
	Workers       int
	FFprobePath   string
	FFmpegPath    string
	Stderr        io.Writer // 进度、警告、错误
	Stdout        io.Writer // 最终摘要
	MaxCandidates int       // 默认 100000
}

// Summary 运行摘要。
type Summary struct {
	Discovered         int
	Renamed, Previewed int
	Unchanged, Failed  int
	Cancelled          int
	Filtered           int
	ScanErrors         int
	Elapsed            time.Duration
	RunDir             string
	GlobalAbort        string // 全局依赖故障原因(空表示无)
}

// discovered = renamed + previewed + unchanged + failed + cancelled
func (s Summary) consistent() bool {
	return s.Discovered == s.Renamed+s.Previewed+s.Unchanged+s.Failed+s.Cancelled
}

// Run 执行完整流水线并返回摘要与退出码。
// ctx 取消(如 Ctrl+C)时:停止取任务、取消子进程与 HTTP、为未完成任务记录 cancelled。
func Run(ctx context.Context, opts Options) (Summary, int) {
	return runWithLogger(ctx, opts, func(dir string) (eventLog, error) { return runlog.Open(dir) })
}

// runWithLogger 允许测试注入日志写入故障；生产始终使用两路 JSONL。
func runWithLogger(ctx context.Context, opts Options, openLog func(string) (eventLog, error)) (Summary, int) {
	ctx, cancelProcessing := context.WithCancel(ctx)
	defer cancelProcessing()
	var outputMu sync.Mutex
	start := time.Now()
	sum := Summary{}
	printf := func(format string, a ...any) {
		outputMu.Lock()
		defer outputMu.Unlock()
		if opts.Stderr != nil {
			fmt.Fprintf(opts.Stderr, format+"\n", a...)
		}
	}
	out := func(format string, a ...any) {
		outputMu.Lock()
		defer outputMu.Unlock()
		if opts.Stdout != nil {
			fmt.Fprintf(opts.Stdout, format+"\n", a...)
		}
	}
	fail2 := func(err error) (Summary, int) {
		printf("错误: %v", err)
		return sum, 2
	}

	// 1. 扫描并冻结任务清单
	maxC := opts.MaxCandidates
	if maxC <= 0 {
		maxC = 100000
	}
	res, err := discovery.Discover(opts.Path, discovery.Options{Recursive: opts.Recursive, MaxCandidates: maxC})
	if err != nil {
		return fail2(fmt.Errorf("扫描失败(INPUT/DISCOVERY): %w", err))
	}
	sum.Discovered = len(res.Jobs)
	sum.Filtered = res.Filtered + res.Symlinks + res.NonRegular + res.Duplicates
	sum.ScanErrors = len(res.ScanErrors)
	for _, se := range res.ScanErrors {
		printf("扫描错误: %s: %s", se.Path, se.Message)
	}
	if sum.Discovered == 0 {
		out("发现 0 个视频")
		sum.Elapsed = time.Since(start)
		if sum.ScanErrors > 0 {
			return sum, 1
		}
		return sum, 0
	}

	// 2. 运行日志与编号
	writer, err := openLog(opts.Config.ResolvedLoggingDir)
	if err != nil {
		return fail2(fmt.Errorf("创建运行日志失败(LOG_WRITE_FAILED): %w", err))
	}
	defer writer.Close()
	sum.RunDir = writer.Dir()
	for i := range res.Jobs {
		res.Jobs[i].JobID = fmt.Sprintf("%04d", i+1)
	}

	mode := "apply"
	if opts.DryRun {
		mode = "dry_run"
	}

	// 3. 共享组件
	prober := &media.Prober{FFprobePath: opts.FFprobePath}
	sampler := media.NewSampler(opts.Config.Sampling)
	extractor := &media.Extractor{
		FFmpegPath: opts.FFmpegPath,
		Images:     opts.Config.Images,
		RunID:      writer.RunID(),
	}
	client := vision.New(opts.Config)

	committer := newCommitter(writer, opts.DryRun, mode, opts.Config)
	var abortReason atomic.Value // string
	var abortOnce sync.Once
	stopped := make(chan struct{})
	abort := func(reason string) {
		abortOnce.Do(func() {
			abortReason.Store(reason)
			close(stopped)
			printf("全局故障,停止调度新任务: %s", reason)
		})
	}
	stopReason := func() string {
		if reason, ok := abortReason.Load().(string); ok {
			return "全局故障: " + reason
		}
		if ctx.Err() != nil {
			return "运行被中断"
		}
		return ""
	}
	committer.onLogFailure = func(err error, ev runlog.Event, hint string) {
		// 日志故障必须取消进行中的外部调用；鉴权故障只停止新任务。
		cancelProcessing()
		abort("LOG_WRITE_FAILED: " + hint)
		printf("日志写入失败(%s), job=%s, source=%s, target=%s: %v", hint, ev.JobID, ev.SourcePath, ev.TargetPath, err)
	}

	// 4. 有界队列 + workers
	queue := make(chan domain.VideoJob, opts.Workers*2)
	var wg sync.WaitGroup
	var done atomic.Int64
	total := len(res.Jobs)
	progress := func(job domain.VideoJob, line string) {
		n := done.Add(1)
		printf("[%d/%d] %s", n, total, line)
	}

	worker := func() {
		for job := range queue {
			if reason := stopReason(); reason != "" {
				committer.cancelled(job, reason)
				progress(job, fmt.Sprintf("cancelled %s", job.Path))
				continue
			}
			rec := processJob(ctx, job, opts, prober, sampler, extractor, client)
			if rec.jobErr != nil && rec.jobErr.Global {
				abort(rec.jobErr.Error())
			}
			// 运行被中断导致的失败按 cancelled 记录(提交临界区由协调器收尾)
			if rec.jobErr != nil && ctx.Err() != nil && rec.jobErr.Code != domain.CodeCancelled {
				rec.jobErr = &domain.JobError{
					Code:    domain.CodeCancelled,
					Message: "运行被中断: " + rec.jobErr.Message,
				}
			}
			committer.commit(ctx, job, rec)
			progress(job, describe(job, rec))
		}
	}
	for range opts.Workers {
		wg.Go(worker)
	}
	for _, job := range res.Jobs {
		if reason := stopReason(); reason != "" {
			committer.cancelled(job, reason)
			progress(job, fmt.Sprintf("cancelled %s", job.Path))
			continue
		}
		select {
		case queue <- job:
			// worker 在取出后再次核对停止状态，不把已排队视为已开始。
		case <-ctx.Done():
			committer.cancelled(job, stopReason())
			progress(job, fmt.Sprintf("cancelled %s", job.Path))
		case <-stopped:
			committer.cancelled(job, stopReason())
			progress(job, fmt.Sprintf("cancelled %s", job.Path))
		}
	}
	close(queue)
	wg.Wait()

	// 5. 汇总
	c := committer.stats()
	sum.Renamed, sum.Previewed, sum.Unchanged = c.renamed, c.previewed, c.unchanged
	sum.Failed, sum.Cancelled = c.failed, c.cancelled
	sum.Elapsed = time.Since(start)
	if reason, ok := abortReason.Load().(string); ok && reason != "" {
		sum.GlobalAbort = reason
	}

	out("发现 %d 个视频;改名 %d,预览 %d,未变 %d,失败 %d,取消 %d",
		sum.Discovered, sum.Renamed, sum.Previewed, sum.Unchanged, sum.Failed, sum.Cancelled)
	out("过滤 %d 项;扫描错误 %d", sum.Filtered, sum.ScanErrors)
	out("总耗时 %s", sum.Elapsed.Round(time.Millisecond))
	out("成功日志: %s", writer.Dir()+string(os.PathSeparator)+"success.jsonl")
	out("失败日志: %s", writer.Dir()+string(os.PathSeparator)+"failure.jsonl")
	if !sum.consistent() {
		printf("内部错误: 结果计数不一致 discovered=%d ≠ %d+%d+%d+%d+%d",
			sum.Discovered, sum.Renamed, sum.Previewed, sum.Unchanged, sum.Failed, sum.Cancelled)
	}

	switch {
	case committer.logFailed:
		printf(" stderr: 日志写入失败,已停止提交;已发生的改名不自动回滚")
		return sum, 2
	case sum.GlobalAbort != "":
		return sum, 2
	case ctx.Err() != nil:
		// 信号中断的退出码由 main 决定(130/143);此处按 1 处理非信号场景
		return sum, 1
	case sum.Failed > 0 || sum.ScanErrors > 0:
		return sum, 1
	default:
		return sum, 0
	}
}

// jobRecord 一个任务各阶段的累积信息。
type jobRecord struct {
	meta     domain.VideoMeta
	frames   domain.FrameSet
	title    domain.TitleResult
	jobErr   *domain.JobError
	stage    domain.Stage
	warnings []string
	started  time.Time
}

// processJob 串联单视频的全部阶段;任何阶段失败即返回。
func processJob(ctx context.Context, job domain.VideoJob, opts Options,
	prober *media.Prober, sampler *media.Sampler, extractor *media.Extractor,
	client *vision.Client) *jobRecord {

	rec := &jobRecord{started: time.Now()}
	jobCtx, cancel := context.WithTimeout(ctx, domain.JobBudget(opts.Config.TimeoutDuration))
	defer cancel()

	mapTimeout := func(jerr *domain.JobError) *domain.JobError {
		if jerr != nil && jobCtx.Err() == context.DeadlineExceeded {
			switch jerr.Code {
			case domain.CodeProbeTimeout, domain.CodeExtractTimeout, domain.CodeAPITimeout, domain.CodeAPIHTTPError:
				return &domain.JobError{Code: domain.CodeJobTimeout, Message: "单视频总预算耗尽: " + jerr.Message}
			}
		}
		return jerr
	}

	// 探测
	rec.stage = domain.StageProbe
	meta, jerr := prober.Probe(jobCtx, job)
	if jerr != nil {
		rec.jobErr = mapTimeout(jerr)
		return rec
	}
	rec.meta = meta
	if meta.HDR {
		rec.warnings = append(rec.warnings, "HDR_NOT_TONEMAPPED: HDR 来源未做色调映射,颜色判断可能有偏差")
	}

	// 采样
	plan, err := sampler.Plan(meta.Duration, meta.SizeBytes)
	if err != nil {
		rec.jobErr = &domain.JobError{Code: domain.CodeExtractFailed, Message: err.Error()}
		return rec
	}

	// 抽帧
	rec.stage = domain.StageExtract
	set, jerr := extractor.Extract(jobCtx, job, meta, plan)
	rec.frames = set
	rec.warnings = append(rec.warnings, set.Warn...)
	for _, fp := range set.Failed {
		rec.warnings = append(rec.warnings, fmt.Sprintf("采样点 %v 失败: %s", fp.Planned, fp.Reason))
	}
	if jerr != nil {
		rec.jobErr = mapTimeout(jerr)
		return rec
	}
	defer func() {
		if set.TempDir != "" {
			if rmErr := os.RemoveAll(set.TempDir); rmErr != nil {
				rec.warnings = append(rec.warnings, fmt.Sprintf("清理临时目录失败: %v", rmErr))
			}
		}
	}()

	// 推理
	rec.stage = domain.StageInference
	title, jerr := client.Title(jobCtx, opts.Config.Prompt, set.Frames)
	rec.title = title
	if jerr != nil {
		if jobCtx.Err() == context.DeadlineExceeded && jerr.Code == domain.CodeCancelled {
			rec.jobErr = &domain.JobError{Code: domain.CodeJobTimeout, Message: "单视频总预算耗尽(等待模型响应)"}
		} else {
			rec.jobErr = jerr
		}
		return rec
	}
	rec.stage = domain.StageNaming
	return rec
}

// describe 生成控制台进度行。
func describe(job domain.VideoJob, rec *jobRecord) string {
	elapsed := time.Since(rec.started).Round(time.Millisecond)
	if rec.jobErr != nil {
		if rec.jobErr.Code == domain.CodeCancelled {
			return fmt.Sprintf("cancelled %s (%s)", job.Path, elapsed)
		}
		return fmt.Sprintf("failed  %s (%s, %s, %s)", job.Path, rec.stage, rec.jobErr.Code, elapsed)
	}
	if rec.title.Title == "" {
		return fmt.Sprintf("error    %s (无标题)", job.Path)
	}
	secs := elapsed.Seconds()
	return fmt.Sprintf("done     %s → %q (%.1fs, %d 帧, %d 次请求)", job.Path, rec.title.Title, secs, rec.title.FramesSent, rec.title.Attempts)
}
