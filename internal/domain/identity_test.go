package domain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotFromInfoPreservesLstatIdentity(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, file, link} {
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := SnapshotFromInfo(info)
		if err != nil {
			t.Fatal(err)
		}
		want, err := StatSnapshot(p)
		if err != nil || !got.Same(want) {
			t.Fatalf("%s: %+v != %+v (%v)", p, got, want, err)
		}
	}
	linked, _ := StatSnapshot(link)
	target, _ := StatSnapshot(file)
	if linked.SameIdentity(target) {
		t.Fatal("followed symlink")
	}
}
