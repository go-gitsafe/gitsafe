package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help", "help"} {
		var out, errb bytes.Buffer
		cmd := RootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if code := cliflags.Run(cmd, []string{arg}); code != 0 {
			t.Errorf("ghmerge %s exited %d\nstderr: %s", arg, code, errb.String())
		}
		for _, want := range []string{"--squash", "--delete-branch", "--token-file"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("ghmerge %s does not name %s:\n%s", arg, want, out.String())
			}
		}
	}
}

// The documented spelling is `ghmerge -squash=false 42`. Read as a cluster it is
// s,q,u,a,s,h -- and the merge would go ahead as a SQUASH, the one thing the
// caller asked it not to do.
func TestBothSpellingsOfEveryFlag(t *testing.T) {
	for _, spelling := range [][]string{
		{"-squash=false", "42"}, {"--squash=false", "42"},
	} {
		cmd := RootCmd()
		if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, spelling)); err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if got, _ := cmd.Flags().GetBool("squash"); got {
			t.Errorf("%q left squash true", spelling)
		}
		if cmd.Flags().NArg() != 1 {
			t.Errorf("%q operands %q", spelling, cmd.Flags().Args())
		}
	}
	cmd := RootCmd()
	if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, []string{"-token-file", "t", "-delete-branch=false", "o/r", "7"})); err != nil {
		t.Fatal(err)
	}
	if tf, _ := cmd.Flags().GetString("token-file"); tf != "t" || cmd.Flags().NArg() != 2 {
		t.Errorf("token-file %q operands %q", tf, cmd.Flags().Args())
	}
	if d, _ := cmd.Flags().GetBool("delete-branch"); d {
		t.Error("-delete-branch=false not read")
	}
}

func TestMisuseIsStatusTwo(t *testing.T) {
	for _, args := range [][]string{nil, {"-nope", "1"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 || strings.TrimSpace(errb.String()) == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errb.String())
		}
	}
}
