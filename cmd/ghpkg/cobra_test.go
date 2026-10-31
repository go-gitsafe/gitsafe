package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

// All four spellings at the root AND for each verb: `ghpkg delete -help` has to
// resolve the subcommand before it rewrites anything, and it is the command that
// deletes things.
func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, prefix := range [][]string{nil, {"list"}, {"delete"}} {
		for _, arg := range []string{"-h", "--help", "-help", "help"} {
			if arg == "help" && len(prefix) > 0 {
				continue
			}
			var out, errb bytes.Buffer
			cmd := RootCmd()
			cmd.SetOut(&out)
			cmd.SetErr(&errb)
			args := append(append([]string{}, prefix...), arg)
			if code := cliflags.Run(cmd, args); code != 0 {
				t.Errorf("ghpkg %v exited %d\nstderr: %s", args, code, errb.String())
			}
			want := "usage:"
			if len(prefix) > 0 {
				want = "--yes"
			}
			if !strings.Contains(out.String(), want) {
				t.Errorf("ghpkg %v does not name %s:\n%s", args, want, out.String())
			}
		}
	}
}

// -yes reaches the flag, wherever it sits, and a -token value that looks like a
// flag stays a value.
func TestSingleDashSpellings(t *testing.T) {
	cmd := RootCmd()
	sub, _, err := cmd.Find([]string{"delete"})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"o", "p", "t", "-yes", "-token", "-weird"}
	if err := sub.Flags().Parse(cliflags.LegacyArgs(cmd, append([]string{"delete"}, args...))[1:]); err != nil {
		t.Fatal(err)
	}
	if y, _ := sub.Flags().GetBool("yes"); !y {
		t.Error("-yes after the operands was not read")
	}
	if tok, _ := sub.Flags().GetString("token"); tok != "-weird" {
		t.Errorf("token = %q", tok)
	}
	if n := sub.Flags().NArg(); n != 3 {
		t.Errorf("operands = %q", sub.Flags().Args())
	}
}

func TestUsageRefusals(t *testing.T) {
	for _, args := range [][]string{nil, {"fix"}, {"list", "a"}, {"delete", "a", "b"}, {"list", "--nope"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 || strings.TrimSpace(errb.String()) == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errb.String())
		}
	}
}
