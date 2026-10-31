package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

// All four spellings at the root AND for backfill: `ghrelease backfill -help`
// has to resolve the subcommand before the adapter rewrites anything, or it is
// judged against the release flags.
func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, tc := range []struct {
		prefix []string
		want   string
	}{{nil, "--notes-file"}, {[]string{"backfill"}, "--inventory"}} {
		for _, arg := range []string{"-h", "--help", "-help", "help"} {
			if arg == "help" && len(tc.prefix) > 0 {
				continue
			}
			var out, errb bytes.Buffer
			cmd := RootCmd()
			cmd.SetOut(&out)
			cmd.SetErr(&errb)
			args := append(append([]string{}, tc.prefix...), arg)
			if code := cliflags.Run(cmd, args); code != 0 {
				t.Errorf("ghrelease %v exited %d\nstderr: %s", args, code, errb.String())
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("ghrelease %v does not name %s:\n%s", args, tc.want, out.String())
			}
		}
	}
}

// The documented invocation: single-dash flags BEFORE the operands, and a value
// that is itself dash-led stays a value.
func TestBothSpellingsOfEveryFlag(t *testing.T) {
	cmd := RootCmd()
	args := []string{"-notes-file", "n.txt", "-dry-run", "-backfill-max", "3", "-no-backfill", "o/r", "68", "v0.1.0"}
	if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, args)); err != nil {
		t.Fatal(err)
	}
	if nf, _ := cmd.Flags().GetString("notes-file"); nf != "n.txt" {
		t.Errorf("notes-file = %q", nf)
	}
	if d, _ := cmd.Flags().GetBool("dry-run"); !d {
		t.Error("-dry-run not read")
	}
	if n, _ := cmd.Flags().GetInt("backfill-max"); n != 3 {
		t.Errorf("backfill-max = %d", n)
	}
	if got := cmd.Flags().Args(); len(got) != 3 || got[0] != "o/r" {
		t.Errorf("operands = %q", got)
	}

	// And on the subcommand.
	root := RootCmd()
	sub, _, err := root.Find([]string{"backfill"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Flags().Parse(cliflags.LegacyArgs(root, []string{"backfill", "-since", "3d", "-dry-run"})[1:]); err != nil {
		t.Fatal(err)
	}
	if s, _ := sub.Flags().GetString("since"); s != "3d" {
		t.Errorf("since = %q", s)
	}
}

func TestMisuseIsStatusTwo(t *testing.T) {
	for _, args := range [][]string{nil, {"-nope"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 || strings.TrimSpace(errb.String()) == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errb.String())
		}
	}
}
