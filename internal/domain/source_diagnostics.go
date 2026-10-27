package domain

import (
	"fmt"
	"strings"
	"time"
)

// Stages at which a source file is checked for changes, passed to clients as
// details.stage of source_changed.
const (
	StageBeforeRename  = "before_rename"  // model returned, rename not committed
	StagePathIdentity  = "path_identity"  // path and ancestor identity check (device and inode only)
	StageAfterExtract  = "after_extract"  // frames extracted, source verified
	StageBeforeRequest = "before_request" // frames extracted and model slot acquired, model not called
)

// SourceChangedError preserves the evidence without weakening source checks.
// message is English; stage is one of the Stage constants.
func SourceChangedError(message, stage, path string, before, after Snapshot) *JobError {
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
		time.Now().UTC().Format(time.RFC3339Nano), stage, path, strings.Join(changed, ", "), before.Dev, after.Dev, before.Ino, after.Ino, before.Size, after.Size, before.ModTime.UTC().Format(time.RFC3339Nano), after.ModTime.UTC().Format(time.RFC3339Nano))}
}
