package discovery

import "testing"

func TestVideoExtensions(t *testing.T) {
	for _, path := range []string{"clip.mp4", "中文视频.MKV", "folder/clip.Mp4", "clip.m2ts"} {
		if !IsVideoExtension(path) {
			t.Errorf("video rejected: %s", path)
		}
	}
	for _, path := range []string{"clip", "clip.mp4.txt", "track.mp3", "photo.jpg", "folder.mp4/file"} {
		if IsVideoExtension(path) {
			t.Errorf("non-video accepted: %s", path)
		}
	}
}
