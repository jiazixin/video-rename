package web

import "embed"

// Assets is produced by npm run build in web. Build the frontend before Go.
//
//go:embed dist
var Assets embed.FS
