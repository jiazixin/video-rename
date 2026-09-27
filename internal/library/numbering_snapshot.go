package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"

	"video-rename/internal/domain"
)

// Stored in the existing fingerprint column. Keep the exact observations used
// for validation, including non-participating files and child directories.
type numberingDirectorySnapshot struct {
	Version int                      `json:"version"`
	Hash    string                   `json:"hash"`
	Entries []numberingEntrySnapshot `json:"entries"`
}
type numberingEntrySnapshot struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	Mtime    string `json:"mtime"`
}

func directoryFingerprint(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	snapshot := numberingDirectorySnapshot{Version: 3, Entries: make([]numberingEntrySnapshot, 0, len(entries))}
	hash := sha256.New()
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return "", err
		}
		snap, err := domain.SnapshotFromInfo(info)
		if err != nil {
			return "", err
		}
		entry := numberingEntrySnapshot{Name: e.Name(), Type: info.Mode().Type().String(), Identity: Identity(snap)}
		// Descendant contents and permission bits are not part of this rename plan.
		if !info.IsDir() {
			entry.Size = info.Size()
			entry.Mtime = stamp(snap)
		}
		fmt.Fprintf(hash, "%q:%q:%q:%d:%q\n", entry.Name, entry.Type, entry.Identity, entry.Size, entry.Mtime)
		snapshot.Entries = append(snapshot.Entries, entry)
	}
	snapshot.Hash = hex.EncodeToString(hash.Sum(nil))
	raw, err := json.Marshal(snapshot)
	return string(raw), err
}
func decodeNumberingSnapshot(raw string) (numberingDirectorySnapshot, bool) {
	var snapshot numberingDirectorySnapshot
	err := json.Unmarshal([]byte(raw), &snapshot)
	return snapshot, err == nil && snapshot.Version == 3 && snapshot.Hash != ""
}
func numberingFingerprintLabel(raw string) string {
	if snapshot, ok := decodeNumberingSnapshot(raw); ok {
		return fmt.Sprintf("v%d:%s（%d 个条目）", snapshot.Version, snapshot.Hash, len(snapshot.Entries))
	}
	if len(raw) > 128 {
		return "无法识别的旧版指纹"
	}
	return raw
}

// Compare the captured snapshots, not a second filesystem read that might
// already reflect another cache refresh. No retry is used to bypass a failure.
func describeNumberingSnapshotDiff(detail *strings.Builder, before, after string) bool {
	old, ok := decodeNumberingSnapshot(before)
	if !ok {
		return false
	}
	current, ok := decodeNumberingSnapshot(after)
	if !ok {
		return false
	}
	byName := make(map[string]numberingEntrySnapshot, len(current.Entries))
	for _, e := range current.Entries {
		byName[e.Name] = e
	}
	count := 0
	report := func(format string, args ...any) {
		if count < 20 {
			fmt.Fprintf(detail, format, args...)
		}
		count++
	}
	for _, a := range old.Entries {
		b, exists := byName[a.Name]
		if !exists {
			report("条目 %q：当前目录列表中缺失；预览类型=%s，身份=%s\n", a.Name, a.Type, a.Identity)
			continue
		}
		delete(byName, a.Name)
		if a != b {
			report("条目 %q：类型 %s → %s；身份(dev:inode) %s → %s；大小 %d → %d；修改时间 %s → %s\n", a.Name, a.Type, b.Type, a.Identity, b.Identity, a.Size, b.Size, a.Mtime, b.Mtime)
		}
	}
	for _, b := range current.Entries {
		if _, exists := byName[b.Name]; exists {
			report("新增条目 %q：类型=%s；身份=%s；大小=%d；修改时间=%s\n", b.Name, b.Type, b.Identity, b.Size, b.Mtime)
		}
	}
	fmt.Fprintf(detail, "总计 %d 个差异条目，最多展示 20 项。以上为预览与本次校验实际读到的值。\n", count)
	if count == 0 {
		fmt.Fprintln(detail, "逐条快照一致，但指纹格式或摘要不同；请重新预览。")
	}
	return true
}
