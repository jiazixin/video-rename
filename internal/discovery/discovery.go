// Package discovery defines the video formats accepted by library scans.
package discovery

import (
	"path/filepath"
	"strings"
)

// videoExtensions lists the built-in video extensions (lowercase, with dot).
var videoExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".mov": true, ".avi": true,
	".webm": true, ".flv": true, ".wmv": true, ".mpg": true, ".mpeg": true,
	".ts": true, ".m2ts": true, ".mts": true, ".3gp": true, ".ogv": true,
	".rmvb": true, ".asf": true, ".vob": true,
}

// IsVideoExtension reports whether ext is a built-in video extension.
func IsVideoExtension(ext string) bool {
	return videoExtensions[filepath.Ext(strings.ToLower(ext))]
}
