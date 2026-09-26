package domain

import (
	"strings"
	"testing"
	"time"
)

func TestSourceChangedErrorEvidence(t *testing.T) {
	before := Snapshot{Dev: 1, Ino: 2, Size: 3, ModTime: time.Unix(100, 123456789)}
	after := Snapshot{Dev: 4, Ino: 5, Size: 6, ModTime: before.ModTime.Add(time.Nanosecond)}
	err := SourceChangedError("source changed", "before inference", "/media/a.mp4", before, after)
	if err.Code != CodeSourceChanged || err.Message != "source changed" {
		t.Fatal(err)
	}
	for _, want := range []string{"before inference", "/media/a.mp4", "差异字段：设备号、inode、大小、修改时间", "1 → 4", "2 → 5", "3 → 6", "123456789Z", "12345679Z", "不是内容哈希比较"} {
		if !strings.Contains(err.ProcessingLog, want) {
			t.Fatalf("missing %q: %s", want, err.ProcessingLog)
		}
	}
}
