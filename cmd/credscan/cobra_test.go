package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

// All four spellings of the question, at the root AND for each verb: the flags
// of `scan` are judged against scan, which is why `credscan clean -help` has to
// resolve the subcommand before it rewrites anything.
func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, prefix := range [][]string{nil, {"scan"}, {"clean"}} {
		for _, arg := range []string{"-h", "--help", "-help", "help"} {
			if arg == "help" && len(prefix) > 0 {
				continue // `credscan scan help` asks scan for a root called help
			}
			var out, errb bytes.Buffer
			cmd := RootCmd()
			cmd.SetOut(&out)
			cmd.SetErr(&errb)
			args := append(append([]string{}, prefix...), arg)
			if code := cliflags.Run(cmd, args); code != 0 {
				t.Errorf("credscan %v exited %d\nstderr: %s", args, code, errb.String())
			}
			want := "scan"
			if len(prefix) > 0 {
				want = "--write"
			}
			if !strings.Contains(out.String(), want) {
				t.Errorf("credscan %v does not name %s:\n%s", args, want, out.String())
			}
		}
	}
}

// The documented spellings are single-dash.
func TestSingleDashSpellingsOfEveryFlag(t *testing.T) {
	for _, tc := range [][]string{
		{"scan", "-verbose", "/nonexistent-root"},
		{"scan", "--verbose", "/nonexistent-root"},
	} {
		code, out, errb := exec1(t, tc...)
		if code != exitInconclusive {
			t.Errorf("%v: exit %d\n%s%s", tc, code, out, errb)
		}
	}
	// -write on scan must reach the refusal, not be read as a cluster.
	code, _, errb := exec1(t, "scan", "-write")
	if code != exitUsage || !strings.Contains(errb, "scan never writes") {
		t.Errorf("scan -write: exit %d, %q", code, errb)
	}
	code, _, errb = exec1(t, "clean", "-write")
	if code != exitUsage || !strings.Contains(errb, "spelled out") {
		t.Errorf("clean -write: exit %d, %q", code, errb)
	}
}
