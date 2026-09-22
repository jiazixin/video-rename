// Package discovery 负责视频文件发现、快照、排序与去重。
// 规则(docs/operations.md 第 1 节):
//   - 目录默认不递归,显式 recursive 才处理子目录;不进入符号链接目录,
//     不处理符号链接文件;只处理普通文件。
//   - 目录中非目标扩展名属于过滤项,只计数;显式输入文件则交给 ffprobe 验证。
//   - 相同路径去重;多个硬链接按文件身份仅保留首个。
//   - 清单按规范化绝对路径排序,支持至多 MaxCandidates 个候选。
package discovery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"video-rename/internal/domain"
)

// videoExtensions 内置视频扩展名列表(小写,含点)。
var videoExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".mov": true, ".avi": true,
	".webm": true, ".flv": true, ".wmv": true, ".mpg": true, ".mpeg": true,
	".ts": true, ".m2ts": true, ".mts": true, ".3gp": true, ".ogv": true,
	".rmvb": true, ".asf": true, ".vob": true,
}

// IsVideoExtension 报告扩展名是否在内置视频列表中。
func IsVideoExtension(ext string) bool {
	return videoExtensions[filepath.Ext(stringsToLower(ext))]
}

func stringsToLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// Options 扫描选项。
type Options struct {
	Recursive     bool
	MaxCandidates int // 超过在任何改名前失败;<=0 视为 1
}

// ScanError 递归过程中单个不可读路径的记录;不终止其他可读路径。
type ScanError struct {
	Path    string
	Message string
}

// Result 扫描结果。
type Result struct {
	Jobs       []domain.VideoJob // 按规范化绝对路径排序
	Filtered   int               // 非目标扩展名
	Symlinks   int               // 跳过的符号链接(文件与目录)
	NonRegular int               // 管道、设备等非普通文件
	Duplicates int               // 同文件身份的硬链接重复项
	ScanErrors []ScanError
}

// Discover 扫描 root 并冻结任务清单。
// 返回的 error 属于启动错误(输入不存在、根不可读、符号链接输入、超上限),
// 应以退出码 2 处理;子目录不可读记录在 Result.ScanErrors,不中断扫描。
func Discover(root string, opts Options) (*Result, error) {
	max := opts.MaxCandidates
	if max <= 0 {
		max = 1
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("无法确定输入绝对路径: %w", err)
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("输入不存在或不可访问: %w", err)
	}
	res := &Result{}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("输入 %s 是符号链接,请指向真实路径", abs)
	case fi.Mode().IsDir():
		if err := scanDir(abs, opts.Recursive, max, res); err != nil {
			return nil, err
		}
	case fi.Mode().IsRegular():
		// 显式输入文件:不按扩展名过滤,由 ffprobe 实际验证
		if err := addCandidate(abs, max, res); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("输入 %s 不是普通文件或目录", abs)
	}
	sort.Slice(res.Jobs, func(i, j int) bool { return res.Jobs[i].Path < res.Jobs[j].Path })
	// 按文件身份去重(硬链接),保留排序后的首个
	seen := map[[2]uint64]bool{}
	deduped := res.Jobs[:0]
	for _, j := range res.Jobs {
		key := [2]uint64{j.Snapshot.Dev, j.Snapshot.Ino}
		if seen[key] {
			res.Duplicates++
			continue
		}
		seen[key] = true
		deduped = append(deduped, j)
	}
	res.Jobs = deduped
	return res, nil
}

// addCandidate 对单个普通文件做快照并纳入清单。
func addCandidate(path string, max int, res *Result) error {
	snap, err := domain.StatSnapshot(path)
	if err != nil {
		return fmt.Errorf("无法读取文件信息 %s: %w", path, err)
	}
	if len(res.Jobs)+1 > max {
		return fmt.Errorf("候选数量超过上限 %d,请分批处理", max)
	}
	res.Jobs = append(res.Jobs, domain.VideoJob{Path: path, Snapshot: snap})
	return nil
}

// scanDir 遍历目录;recursive 控制是否进入子目录。
func scanDir(root string, recursive bool, max int, res *Result) error {
	if recursive {
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if path == root {
					return err // 根目录不可读属于启动错误
				}
				res.ScanErrors = append(res.ScanErrors, ScanError{Path: path, Message: err.Error()})
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if path == root {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				res.Symlinks++
				if d.IsDir() {
					return fs.SkipDir // 符号链接目录不进入
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			fi, ferr := d.Info()
			if ferr != nil {
				res.ScanErrors = append(res.ScanErrors, ScanError{Path: path, Message: ferr.Error()})
				return nil
			}
			return visitEntry(path, fi, max, res)
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("根目录不可读: %w", err)
	}
	for _, e := range entries {
		full := filepath.Join(root, e.Name())
		fi, err := e.Info()
		if err != nil {
			res.ScanErrors = append(res.ScanErrors, ScanError{Path: full, Message: err.Error()})
			continue
		}
		if err := visitEntry(full, fi, max, res); err != nil {
			return err
		}
	}
	return nil
}

// visitEntry 处理目录项:过滤扩展名与非普通文件,合格者纳入清单。
func visitEntry(path string, fi fs.FileInfo, max int, res *Result) error {
	switch {
	case fi.IsDir():
		// 目录项本身不处理;是否进入由上层决定
	case fi.Mode()&os.ModeSymlink != 0:
		res.Symlinks++
	case fi.Mode().IsRegular():
		if !IsVideoExtension(fi.Name()) {
			res.Filtered++
			return nil
		}
		return addCandidate(path, max, res)
	default:
		res.NonRegular++ // 管道、设备、套接字等
	}
	return nil
}
