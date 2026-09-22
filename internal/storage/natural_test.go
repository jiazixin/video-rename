package storage

import "testing"

func TestNaturalName(t *testing.T) {
	for _, pair := range [][2]string{{"1.mp4", "2.mp4"}, {"2.mp4", "10.mp4"}, {"10.mp4", "20.mp4"}, {"视频2片段9", "视频2片段10"}, {"999999999999999999999999", "1000000000000000000000000"}, {"clip0002", "clip10"}, {"clip02", "clip2"}} {
		if naturalName(pair[0], pair[1]) >= 0 || naturalName(pair[1], pair[0]) <= 0 {
			t.Fatalf("unexpected order: %q", pair)
		}
	}
	if naturalName("视频02.mp4", "视频02.mp4") != 0 {
		t.Fatal("unequal identical name")
	}
}
