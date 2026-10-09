package main

import (
	"cmp"
	"runtime/debug"
	"strings"
)

// Builds may override the Go module version with -ldflags '-X main.version=<release>'.
// Ordinary Git builds use the module version embedded by Go.
var version string

func versionString() string {
	info, _ := debug.ReadBuildInfo()
	return formatVersion(version, info)
}

func formatVersion(release string, info *debug.BuildInfo) string {
	var moduleVersion, revision string
	var modified bool
	if info != nil {
		moduleVersion = info.Main.Version
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	revision = revision[:min(12, len(revision))]
	release = cmp.Or(usableVersion(release), usableVersion(moduleVersion), revision, "dev")
	var details []string
	if revision != "" {
		details = append(details, "revision="+revision)
	}
	if modified {
		details = append(details, "modified")
	}
	if len(details) == 0 {
		return release
	}
	return release + " (" + strings.Join(details, ", ") + ")"
}

func usableVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "dev" || value == "(devel)" {
		return ""
	}
	return value
}
