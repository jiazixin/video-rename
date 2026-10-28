package domain

import (
	"strings"
	"testing"
	"time"
)

func TestSourceChangedErrorEvidence(t *testing.T) {
	before := Snapshot{Dev: 1, Ino: 2, Size: 3, ModTime: time.Unix(100, 123456789)}
	after := Snapshot{Dev: 4, Ino: 5, Size: 6, ModTime: before.ModTime.Add(time.Nanosecond)}
	err := SourceChangedError(SourceChange{Message: "source changed", Stage: StageBeforeRequest, Path: "/media/a.mp4", Before: before, After: after})
	if err.Code != CodeSourceChanged || err.Message != "source changed" || err.Details["stage"] != StageBeforeRequest {
		t.Fatal(err)
	}
	for _, want := range []string{StageBeforeRequest, "/media/a.mp4", "changed fields: device, inode, size, mtime", "1 -> 4", "2 -> 5", "3 -> 6", "123456789Z", "12345679Z", "not content hashes"} {
		if !strings.Contains(err.ProcessingLog, want) {
			t.Fatalf("missing %q: %s", want, err.ProcessingLog)
		}
	}
}
