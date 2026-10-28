package library

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/timefmt"
)

// numberingErrorRecord keeps the error record and its diagnostic together in
// the TEXT column.
type numberingErrorRecord struct {
	Code          string         `json:"code,omitzero"`
	Message       string         `json:"message"`
	Details       map[string]any `json:"details,omitzero"`
	ProcessingLog string         `json:"processing_log,omitzero"`
}

func (b Numbering) numberingErrorRecord() string {
	if b.ProcessingLog == "" {
		return b.Error.Text()
	}
	data, err := json.Marshal(numberingErrorRecord{b.Error.Code, b.Error.Message, b.Error.Details, b.ProcessingLog})
	if err != nil {
		return b.Error.Text()
	}
	return string(data)
}

// decodeNumberingError fills Error and ProcessingLog from the stored text.
func (b *Numbering) decodeNumberingError(stored string) error {
	if stored == "" {
		return nil
	}
	var record numberingErrorRecord
	if err := json.Unmarshal([]byte(stored), &record); err != nil {
		return fmt.Errorf("numbering %s error record: %w", b.ID, err)
	}
	if record.Code == "" {
		return fmt.Errorf("numbering %s error record lacks a code", b.ID)
	}
	b.Error = domain.Record{Code: record.Code, Message: record.Message, Details: record.Details}
	b.ProcessingLog = record.ProcessingLog
	return nil
}

// numberingMoveFailureOptions groups the inputs for numberingMoveFailure.
type numberingMoveFailureOptions struct {
	Batch  *Numbering
	Item   *NumberingItem
	Path   string
	Target string
	Cause  *domain.JobError
}

// Capture after the failed syscall and before any rollback. These are subsequent
// observations, not an assertion about the filesystem at syscall time.
func numberingMoveFailure(params numberingMoveFailureOptions) error {
	b, item, path, target, cause := params.Batch, params.Item, params.Path, params.Target, params.Cause

	phase := "final_rename"
	if target == item.Temp {
		phase = "temporary_rename"
	}
	if b.Status == "rollback" {
		phase = "rollback_rename"
	}
	var detail strings.Builder
	fmt.Fprintf(&detail, "numbering failure\ncaptured: %s\nbatch: %s\ndirectory: %q\nphase: %s\noriginal name: %q\ncurrent name: %q\ntarget name: %q\nfinal name: %q\nsystem error: %v\npreview identity: %s; size: %d; mtime_ns: %d\n", timefmt.Format(time.Now()), b.ID, path, phase, item.Old, item.Current, target, item.New, cause, item.Identity, item.Size, item.ModifiedNS)
	inspect := func(label, name string) {
		info, err := os.Lstat(filepath.Join(path, name))
		if err != nil {
			fmt.Fprintf(&detail, "%s %q: %v\n", label, name, err)
			return
		}
		fmt.Fprintf(&detail, "%s %q: kind=%s; size=%d; mtime_ns=%d", label, name, kindOf(info), info.Size(), info.ModTime().UnixNano())
		if snapshot, err := domain.SnapshotFromInfo(info); err == nil {
			fmt.Fprintf(&detail, "; identity=%s", Identity(snapshot))
		}
		fmt.Fprintln(&detail)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(filepath.Join(path, name))
			fmt.Fprintf(&detail, "link target: %q; read error: %v\n", link, err)
		}
	}
	inspect("source", item.Current)
	inspect("target", target)
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Fprintf(&detail, "read directory: %v\n", err)
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
			inspect("entry matching the target (case-insensitive)", entry.Name())
			participation := "not part of this numbering"
			for _, planned := range b.Items {
				if entry.Name() == planned.Current || entry.Name() == planned.Old || entry.Name() == planned.New {
					participation = fmt.Sprintf("planned %q -> %q; recorded at %q", planned.Old, planned.New, planned.Current)
					break
				}
			}
			fmt.Fprintln(&detail, participation)
		}
		fmt.Fprintf(&detail, "entries matching the target: %d (at most 20 shown)\n", count)
	}
	fmt.Fprintln(&detail, "These reads happened after the failed rename call and before recovery; they do not establish a cache fault or external change.")
	details := map[string]any{"phase": phase, "from": item.Current, "to": target}
	for key, value := range cause.Details {
		details[key] = value
	}
	return &domain.JobError{Code: cause.Code, Message: fmt.Sprintf("%s failed: %q -> %q: %s", phase, item.Current, target, cause.Message), Details: details, ProcessingLog: detail.String()}
}
