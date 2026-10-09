package main

import (
	"runtime/debug"
	"testing"
)

func TestFormatVersion(t *testing.T) {
	for _, tc := range []struct {
		name, release, want string
		info                *debug.BuildInfo
	}{
		{name: "source archive", release: "dev", want: "dev"},
		{name: "no build info", want: "dev"},
		{name: "empty override", release: "  ", want: "dev"},
		{name: "explicit release", release: "1.2.3", want: "1.2.3"},
		{name: "trim override", release: "  v1.2.3  ", want: "v1.2.3"},
		{name: "development module", info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, want: "dev"},
		{name: "tagged module", info: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, want: "v1.2.3"},
		{name: "module pseudo-version", info: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.4-0.20261009000000-0123456789ab"}}, want: "v1.2.4-0.20261009000000-0123456789ab"},
		{name: "override module version", release: "v1.2.3", info: &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261009000000-0123456789ab"}}, want: "v1.2.3"},
		{name: "explicit dev override", release: "dev", info: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, want: "v1.2.3"},
		{name: "explicit devel override", release: " (devel) ", info: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, want: "v1.2.3"},
		{name: "devel without build info", release: "(devel)", want: "dev"},
		{name: "empty override uses module", release: "  ", info: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, want: "v1.2.3"},
		{name: "no VCS metadata", release: "dev", info: &debug.BuildInfo{}, want: "dev"},
		{
			name: "clean checkout", release: "dev", want: "0123456789ab (revision=0123456789ab)",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
				{Key: "vcs.modified", Value: "false"},
			}},
		},
		{
			name: "modified release checkout", release: "1.2.3", want: "1.2.3 (revision=0123456789ab, modified)",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.modified", Value: "true"},
				{Key: "vcs.revision", Value: "0123456789abcdef"},
			}},
		},
		{
			name: "modified module tag", want: "v1.2.3+dirty (revision=0123456789ab, modified)",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3+dirty"}, Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef"},
				{Key: "vcs.modified", Value: "true"},
			}},
		},
		{
			name: "commits after tag", want: "v1.2.4-0.20261009000000-0123456789ab (revision=0123456789ab)",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.4-0.20261009000000-0123456789ab"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}},
		},
		{
			name: "short revision", release: "dev", want: "abc (revision=abc)",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}},
		},
		{
			name: "devel module falls back to revision", want: "0123456789ab (revision=0123456789ab, modified)",
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef"},
				{Key: "vcs.modified", Value: "true"},
			}},
		},
		{
			name: "dev module falls back to revision", want: "abc (revision=abc)",
			info: &debug.BuildInfo{Main: debug.Module{Version: "dev"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}},
		},
		{
			name: "modified without revision", release: "dev", want: "dev (modified)",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatVersion(tc.release, tc.info); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
