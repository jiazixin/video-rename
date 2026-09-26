// Package discovery defines the video formats accepted by library scans.
package discovery

import (
	"path/filepath"
	"strings"
)

// videoExtensions 内置视频扩展名列表(小写,含点)。
var videoExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".mov": true, ".avi": true,
	".webm": true, ".flv": true, ".wmv": true, ".mpg": true, ".mpeg": true,
	".ts": true, ".m2ts": true, ".mts": true, ".3gp": true, ".ogv": true,
	".rmvb": true, ".asf": true, ".vob": true,
}

// IsVideoExtension 报告扩展名是否在内置视频列表中。
func IsVideoExtension(ext string) bool {
	return videoExtensions[filepath.Ext(strings.ToLower(ext))]
}
