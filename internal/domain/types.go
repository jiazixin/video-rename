// Package domain 定义各模块之间交换的共享任务数据、状态与错误码。
// 模块职责边界见 docs/plans/2026-09-17-video-rename-design.md:
// 媒体模块不访问 HTTP,模型模块不改文件,命名模块不执行 rename。
package domain

import (
	"fmt"
	"time"
)

// Stage 标识任务结束时所处的处理阶段。
type Stage string

const (
	StageDiscovery Stage = "discovery"
	StageProbe     Stage = "probe"
	StageExtract   Stage = "extract"
	StageInference Stage = "inference"
	StageNaming    Stage = "naming"
	StageCommit    Stage = "rename"
)

// Status 任务最终状态;每个纳入清单的任务恰好有一个最终结果。
type Status string

const (
	StatusRenamed   Status = "renamed"
	StatusPreviewed Status = "previewed"
	StatusUnchanged Status = "unchanged"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// ErrorCode 错误码,分类与 docs/operations.md 第 6 节保持一致。
type ErrorCode string

const (
	// 启动/扫描
	CodeConfigInvalid   ErrorCode = "CONFIG_INVALID"
	CodeToolNotFound    ErrorCode = "TOOL_NOT_FOUND"
	CodeInputNotFound   ErrorCode = "INPUT_NOT_FOUND"
	CodeDiscoveryFailed ErrorCode = "DISCOVERY_FAILED"
	CodeLogWriteFailed  ErrorCode = "LOG_WRITE_FAILED"

	// 探测
	CodeProbeExecFailed      ErrorCode = "PROBE_EXEC_FAILED"
	CodeProbeTimeout         ErrorCode = "PROBE_TIMEOUT"
	CodeProbeOutputLimit     ErrorCode = "PROBE_OUTPUT_LIMIT"
	CodeProbeJSONInvalid     ErrorCode = "PROBE_JSON_INVALID"
	CodeProbeNoVideo         ErrorCode = "PROBE_NO_VIDEO"
	CodeProbeDurationUnknown ErrorCode = "PROBE_DURATION_UNKNOWN"

	// 抽帧
	CodeExtractFailed             ErrorCode = "EXTRACT_FAILED"
	CodeExtractTimeout            ErrorCode = "EXTRACT_TIMEOUT"
	CodeExtractInsufficientFrames ErrorCode = "EXTRACT_INSUFFICIENT_FRAMES"
	CodeImageTooLarge             ErrorCode = "IMAGE_TOO_LARGE"

	// HTTP
	CodeRequestTooLarge     ErrorCode = "REQUEST_TOO_LARGE"
	CodeAPITimeout          ErrorCode = "API_TIMEOUT"
	CodeAPIUnauthorized     ErrorCode = "API_UNAUTHORIZED"
	CodeAPIRateLimited      ErrorCode = "API_RATE_LIMITED"
	CodeAPIHTTPError        ErrorCode = "API_HTTP_ERROR"
	CodeAPIResponseInvalid  ErrorCode = "API_RESPONSE_INVALID"
	CodeAPIResponseTooLarge ErrorCode = "API_RESPONSE_TOO_LARGE"

	// 命名/提交
	CodeTitleInvalid            ErrorCode = "TITLE_INVALID"
	CodeNamingFailed            ErrorCode = "NAMING_FAILED"
	CodeModelOutputTruncated    ErrorCode = "MODEL_OUTPUT_TRUNCATED"
	CodeSourceChanged           ErrorCode = "SOURCE_CHANGED"
	CodeRenameConflict          ErrorCode = "RENAME_CONFLICT"
	CodeRenameConflictExhausted ErrorCode = "RENAME_CONFLICT_EXHAUSTED"
	CodeRenameUnsupported       ErrorCode = "RENAME_UNSUPPORTED"
	CodeRenamePermissionDenied  ErrorCode = "RENAME_PERMISSION_DENIED"
	CodeRenameFailed            ErrorCode = "RENAME_FAILED"

	// 生命周期
	CodeJobTimeout ErrorCode = "JOB_TIMEOUT"
	CodeCancelled  ErrorCode = "CANCELLED"
)

// JobError 携带错误码以及媒体进程或 HTTP 上下文信息。
// Global 为 true 表示全局依赖故障(鉴权失效、日志不可写、外部工具消失),
// 会停止调度新任务;普通视频级错误不终止批次。
type JobError struct {
	RawRequest  string `json:"-"`
	RawResponse string `json:"-"`
	Code        ErrorCode
	Message     string
	ExitCode    int    // 媒体进程退出码;未运行或未知为 0
	HTTPStatus  int    // HTTP 状态码;非 HTTP 错误为 0
	StderrTail  string // 裁剪后的 stderr 或脱敏响应摘要
	Global      bool
}

func (e *JobError) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := fmt.Sprintf("%s: %s", e.Code, e.Message)
	if e.ExitCode != 0 {
		msg += fmt.Sprintf(" (exit code %d)", e.ExitCode)
	}
	if e.HTTPStatus != 0 {
		msg += fmt.Sprintf(" (http %d)", e.HTTPStatus)
	}
	if e.StderrTail != "" {
		msg += fmt.Sprintf(" (stderr: %s)", e.StderrTail)
	}
	return msg
}

// Snapshot 源文件身份快照:设备与索引节点、大小和修改时间。
// 用于去重,以及提交前核验源文件未被外部修改。
type Snapshot struct {
	Dev     uint64
	Ino     uint64
	Size    int64
	ModTime time.Time
}

// Same 报告两份快照是否指向同一身份且内容未变。
func (s Snapshot) Same(o Snapshot) bool {
	return s.Dev == o.Dev && s.Ino == o.Ino && s.Size == o.Size && s.ModTime.Equal(o.ModTime)
}

// SameIdentity 仅比较文件系统身份(设备 + 索引节点),
// 用于同目录候选目标指向源文件本身(含大小写不敏感卷)的 unchanged 判定。
func (s Snapshot) SameIdentity(o Snapshot) bool {
	return s.Dev == o.Dev && s.Ino == o.Ino
}

// VideoJob 待处理视频任务,包含规范化绝对路径与扫描时的身份快照。
type VideoJob struct {
	JobID    string
	Path     string
	Snapshot Snapshot
}

// VideoMeta ffprobe 探测出的与后续阶段相关的元信息。
type VideoMeta struct {
	StreamIndex   int // 选中的视频流在输入中的绝对流索引
	Duration      time.Duration
	SizeBytes     int64
	Width         int // 编码尺寸
	Height        int
	SARNum        int // sample_aspect_ratio 分子;缺失或 0:1 按方形像素
	SARDen        int
	Rotation      int // 度数,取 0/90/180/270
	DisplayWidth  int // 应用 SAR 与旋转后的显示尺寸
	DisplayHeight int
	HDR           bool // 检测到 HDR 传递特性,仅用于 warning
	HDRNotToneMap bool
}

// FramePlan 抽帧计划:时间点与复盘信息。
type FramePlan struct {
	Duration    time.Duration   // 原始时长
	BaseN       int             // 规则给出的基础帧数 N0
	TargetN     int             // 最终目标帧数 N
	WindowStart time.Duration   // 有效窗口起点 A
	WindowLen   time.Duration   // 有效窗口长度 L
	Times       []time.Duration // 每段中点时间点,长度为 TargetN
}

// Frame 单张已成功提取并通过校验的 JPEG 截图。
type Frame struct {
	Planned      time.Duration // 原计划时间点(中点)
	Actual       time.Duration // 实际请求时间点(补试后可能与计划不同)
	Path         string        // 临时文件绝对路径
	Width        int
	Height       int
	Bytes        int
	Recompressed bool // 因超过 1 MiB 上限重新编码过
}

// FailedPoint 单个采样点失败记录,进入 warnings。
type FailedPoint struct {
	Planned time.Duration
	Actual  time.Duration
	Reason  string
}

// FrameSet 一个视频的抽帧结果。TempDir 指向私有临时目录,
// 调用方在视频完成(含失败与取消)后负责清理。
type FrameSet struct {
	Plan    FramePlan
	Frames  []Frame
	Failed  []FailedPoint
	Warn    []string // 阶段级 warning(降级、超时、清理失败等)
	TempDir string
}

// TitleResult 模型返回并通过输出验证的标题。
type TitleResult struct {
	RawRequest  string `json:"-"`
	RawResponse string `json:"-"`
	Title       string
	Attempts    int // HTTP 尝试次数
	FramesSent  int // 实际发送帧数(预算裁剪后)
}

// RequestBudgets 内置的资源保护上限。
// 这些是设计文档固定的保护参数,不暴露为配置。
type RequestBudgets struct {
	ProbeTimeout     time.Duration // ffprobe 30s
	ProbeStdoutLimit int64         // 4 MiB
	ExtractBudget    time.Duration // 抽帧总预算 120s
	ImageLimit       int64         // 单图 1 MiB
	RequestBodyLimit int64         // 请求体 16 MiB
	ResponseBodyLim  int64         // 响应体 1 MiB
	StderrTailLimit  int           // stderr 保留 8 KiB
	ResponseSummary  int           // 错误响应摘要 2 KiB
}

// DefaultBudgets 返回设计文档规定的内置保护参数。
func DefaultBudgets() RequestBudgets {
	return RequestBudgets{
		ProbeTimeout:     30 * time.Second,
		ProbeStdoutLimit: 4 * 1024 * 1024,
		ExtractBudget:    120 * time.Second,
		ImageLimit:       1024 * 1024,
		RequestBodyLimit: 16 * 1024 * 1024,
		ResponseBodyLim:  1024 * 1024,
		StderrTailLimit:  8 * 1024,
		ResponseSummary:  2 * 1024,
	}
}

// JobBudget 计算单视频总预算:
// 30s 探测 + 120s 抽帧 + 3 × server.timeout + 30s 退避 + 30s 收尾。
func JobBudget(serverTimeout time.Duration) time.Duration {
	return 30*time.Second + 120*time.Second + 3*serverTimeout + 30*time.Second + 30*time.Second
}
