// Package runlog 实现每次运行的独立日志目录与两路 JSONL:
// success.jsonl 承载 renamed/previewed/unchanged 最终事件;
// failure.jsonl 承载失败、取消、运行级故障,以及用于崩溃核对的
// rename_pending 审计事件(record_type=journal,不计入失败统计)。
// 日志不保存 API key、base64 图片、完整 prompt 或完整模型原始响应。
package runlog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SchemaVersion 当前日志结构版本。
const SchemaVersion = 1

// Metadata 结果 metadata 字段。
type Metadata struct {
	SizeBytes       int64   `json:"size_bytes,omitzero"`
	DurationSeconds float64 `json:"duration_seconds,omitzero"`
	Width           int     `json:"width,omitzero"`
	Height          int     `json:"height,omitzero"`
}

// SourceSnapshot 保存扫描时身份，供未确认 pending 的人工核对使用。
// 设备与 inode 用十进制字符串，避免 JSON 消费端浮点数精度丢失。
type SourceSnapshot struct {
	Dev     uint64    `json:"dev,string"`
	Ino     uint64    `json:"ino,string"`
	Size    int64     `json:"size_bytes"`
	ModTime time.Time `json:"mtime"`
}

// Event 单条 JSONL 事件;路径使用 JSON 转义,不拼字符串。
type Event struct {
	SchemaVersion  int             `json:"schema_version"`
	Time           string          `json:"time"` // UTC RFC3339
	RunID          string          `json:"run_id"`
	JobID          string          `json:"job_id"`
	Mode           string          `json:"mode"`        // apply | dry_run
	RecordType     string          `json:"record_type"` // result | journal
	Status         string          `json:"status"`
	SourcePath     string          `json:"source_path"`
	SourceSnapshot *SourceSnapshot `json:"source_snapshot,omitempty"`
	TargetPath     string          `json:"target_path,omitempty"`
	Stage          string          `json:"stage,omitempty"`
	ElapsedMS      int64           `json:"elapsed_ms,omitzero"`
	Metadata       Metadata        `json:"metadata,omitzero"`
	FramesPlanned  int             `json:"frames_planned,omitzero"`
	FramesSent     int             `json:"frames_sent,omitzero"`
	HTTPAttempts   int             `json:"http_attempts,omitzero"`
	Warnings       []string        `json:"warnings,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	ExitCode       int             `json:"exit_code,omitzero"`
	HTTPStatus     int             `json:"http_status,omitzero"`
	StderrTail     string          `json:"stderr_tail,omitempty"`
	AbortReason    string          `json:"abort_reason,omitempty"`
	Model          string          `json:"model,omitempty"`
	PromptHash     string          `json:"prompt_hash,omitempty"`
}

// Writer 两路 JSONL 写入器;线程安全。
// 任一写入失败即进入 broken 状态,后续写入立即报错,
// 由上层停止新提交(退出码 2)。
type Writer struct {
	mu      sync.Mutex
	runID   string
	dir     string
	success *os.File
	failure *os.File
	broken  error
}

// Open 创建 <baseDir>/<UTC时间>-<run_id>/ 及两份 JSONL 文件。
func Open(baseDir string) (*Writer, error) {
	var rid [6]byte
	if _, err := rand.Read(rid[:]); err != nil {
		return nil, fmt.Errorf("生成 run_id 失败: %w", err)
	}
	runID := hex.EncodeToString(rid[:])
	dir := filepath.Join(baseDir, time.Now().UTC().Format("20060102T150405Z")+"-"+runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建运行日志目录失败: %w", err)
	}
	w := &Writer{runID: runID, dir: dir}
	var err error
	if w.success, err = os.OpenFile(filepath.Join(dir, "success.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
		return nil, fmt.Errorf("创建 success.jsonl 失败: %w", err)
	}
	if w.failure, err = os.OpenFile(filepath.Join(dir, "failure.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
		w.success.Close()
		return nil, fmt.Errorf("创建 failure.jsonl 失败: %w", err)
	}
	return w, nil
}

// RunID 返回本次运行标识。
func (w *Writer) RunID() string { return w.runID }

// Dir 返回运行目录绝对路径。
func (w *Writer) Dir() string { return w.dir }

// Pending 将 rename_pending 审计事件写入 failure 并强制 sync;
// 写成功后才允许执行 rename。
func (w *Writer) Pending(ev Event) error {
	ev.SchemaVersion = SchemaVersion
	ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	ev.RunID = w.runID
	ev.RecordType = "journal"
	ev.Status = "pending"
	return w.write(w.failure, ev, true)
}

// Success 写入成功最终事件。
func (w *Writer) Success(ev Event) error {
	ev.SchemaVersion = SchemaVersion
	ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	ev.RunID = w.runID
	ev.RecordType = "result"
	return w.write(w.success, ev, false)
}

// Failure 写入失败最终事件。
func (w *Writer) Failure(ev Event) error {
	ev.SchemaVersion = SchemaVersion
	ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	ev.RunID = w.runID
	ev.RecordType = "result"
	return w.write(w.failure, ev, true)
}

// writeLine 序列化并写入单行;sync 强制时执行 fsync。
func (w *Writer) write(f *os.File, ev Event, sync bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken != nil {
		return w.broken
	}
	line, err := json.Marshal(ev)
	if err != nil {
		w.broken = fmt.Errorf("日志事件序列化失败: %w", err)
		return w.broken
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		w.broken = fmt.Errorf("日志写入失败: %w", err)
		return w.broken
	}
	if sync {
		if err := f.Sync(); err != nil {
			w.broken = fmt.Errorf("日志 sync 失败: %w", err)
			return w.broken
		}
	}
	return nil
}

// Broken 报告写入器是否已进入故障状态。
func (w *Writer) Broken() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.broken
}

// Close 关闭两份文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var first error
	if w.success != nil {
		if err := w.success.Close(); err != nil && first == nil {
			first = err
		}
	}
	if w.failure != nil {
		if err := w.failure.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
