package main

import "testing"

func TestArgs(t *testing.T) {
	for _, args := range [][]string{{}, {"-f", "a.yaml"}, {"--config=a.yaml"}, {"-fa.yaml"}, {"--help"}, {"--version"}} {
		if _, e := parseArgs(args); e != nil {
			t.Fatalf("%v: %v", args, e)
		}
	}
	for _, args := range [][]string{{"--path", "/media"}, {"--recursive"}, {"--dry-run"}, {"--workers", "2"}, {"--config"}, {"--config="}, {"-f", "a", "--config", "b"}, {"position"}, {"--help=false"}} {
		if _, e := parseArgs(args); e == nil {
			t.Fatalf("应拒绝 %v", args)
		}
	}
}
