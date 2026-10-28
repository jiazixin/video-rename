package naming

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzCleanTitle(f *testing.F) {
	for _, title := range []string{"title", "../CON.mp4", "\x00\r\n", "中文🎬", "a/b\\c"} {
		f.Add(title)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1<<16 {
			return
		}
		title, err := CleanTitle(input, ".mp4")
		if err != nil {
			return
		}
		if title == "" || !utf8.ValidString(title) || strings.ContainsAny(title, "/\\\x00") {
			t.Fatalf("got unsafe title %q, want valid single filename", title)
		}
	})
}
