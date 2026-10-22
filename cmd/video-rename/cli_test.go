package main

import "testing"

func TestArgs(t *testing.T) {
	for _, args := range [][]string{{}, {"-c", "a.yaml"}, {"-c=a.yaml"}, {"--config", "a.yaml"}, {"--config=a.yaml"}, {"-ca.yaml"}, {"--help"}, {"--version"}} {
		opts, e := parseArgs(args)
		if e != nil {
			t.Fatalf("%v: %v", args, e)
		}
		wantPath := defaultConfigPath
		if len(args) > 0 && args[0] != "--help" && args[0] != "--version" {
			wantPath = "a.yaml"
		}
		if opts.ConfigPath != wantPath {
			t.Fatalf("%v: ConfigPath = %q, want %q", args, opts.ConfigPath, wantPath)
		}
	}
	for _, args := range [][]string{{"--path", "/media"}, {"--recursive"}, {"--dry-run"}, {"--workers", "2"}, {"--config"}, {"--config="}, {"-c"}, {"-c="}, {"-f", "a.yaml"}, {"-f=a.yaml"}, {"-fa.yaml"}, {"-c", "a", "--config", "b"}, {"position"}, {"--help=false"}} {
		if _, e := parseArgs(args); e == nil {
			t.Fatalf("应拒绝 %v", args)
		}
	}
}
