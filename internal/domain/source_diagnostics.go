package domain

import (
	"fmt"
	"strings"
	"time"

	"video-rename/internal/timefmt"
)

// Stages at which a source file is checked for changes, passed to clients as
// details.stage of source_changed.
const (
	StageBeforeRename  = "before_rename"  // model returned, rename not committed
	StagePathIdentity  = "path_identity"  // path and ancestor identity check (device and inode only)
	StageAfterExtract  = "after_extract"  // frames extracted, source verified
	StageBeforeRequest = "before_request" // frames extracted and model slot acquired, model not called
)

// SourceChange is the captured evidence for a failed source identity check.
type SourceChange struct {
	Message string
	Stage   string
	Path    string
	Before  Snapshot
	After   Snapshot
}

// SourceChangedError preserves the evidence without weakening source checks.
func SourceChangedError(change SourceChange) *JobError {
	message, stage, path, before, after := change.Message, change.Stage, change.Path, change.Before, change.After
	var changed []string
	if before.Dev != after.Dev {
		changed = append(changed, "device")
	}
	if before.Ino != after.Ino {
		changed = append(changed, "inode")
	}
	if before.Size != after.Size {
		changed = append(changed, "size")
	}
	if !before.ModTime.Equal(after.ModTime) {
		changed = append(changed, "mtime")
	}
	return &JobError{Code: CodeSourceChanged, Message: message, Details: map[string]any{"stage": stage, "changed": changed}, ProcessingLog: fmt.Sprintf(
		"source file check\nchecked (UTC): %s\nstage: %s\npath: %q\nchanged fields: %s\ndevice: %d -> %d\ninode: %d -> %d\nsize (bytes): %d -> %d\nmtime (UTC, ns): %s -> %s\nnote: this compares file system metadata, not content hashes; a difference alone does not prove the video content changed.\n",
		timefmt.Format(time.Now()), stage, path, strings.Join(changed, ", "), before.Dev, after.Dev, before.Ino, after.Ino, before.Size, after.Size, before.ModTime.UTC().Format(time.RFC3339Nano), after.ModTime.UTC().Format(time.RFC3339Nano))}
}
