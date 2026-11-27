package main

import (
	"reflect"
	"testing"
)

// ghrelease merges through ghmerge, whose release gate refuses a merge that
// nothing tags. ghrelease IS the tag, so it says so with --no-release, and must
// not be refused by its own gate.
func TestMergeArgsMarkTheReleasePath(t *testing.T) {
	got := mergeArgs([]string{"--issues-waiver", "tracking"}, "o/r", 7)
	want := []string{"--issues-waiver", "tracking", "--no-release", "ghrelease tags this merge", "o/r", "7"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := mergeArgs(nil, "o/r", 7); !reflect.DeepEqual(got, []string{"--no-release", "ghrelease tags this merge", "o/r", "7"}) {
		t.Errorf("no flags: %q", got)
	}
}

// The caller's slice must come back as it went in: appending in place would
// leak the marker into the flags of the next call.
func TestMergeArgsDoNotTouchTheCallersFlags(t *testing.T) {
	flags := make([]string, 2, 16)
	flags[0], flags[1] = "--evidence", "u"
	_ = mergeArgs(flags, "o/r", 1)
	if got := flags[:cap(flags)][2]; got != "" {
		t.Errorf("the caller's backing array was written: %q", got)
	}
}
