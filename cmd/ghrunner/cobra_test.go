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
			t.Errorf("ghrunner %s exited %d\nstderr: %s", arg, code, errb.String())
		}
		for _, want := range []string{"--host", "--name", "--labels", "--group", "--work", "--remove"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("ghrunner %s does not name %s:\n%s", arg, want, out.String())
			}
		}
	}
}

func TestBothSpellingsOfEveryFlag(t *testing.T) {
	for _, spelling := range [][]string{
		{"-host", "u@m"}, {"--host", "u@m"}, {"-host=u@m"}, {"--host=u@m"},
	} {
		cmd := RootCmd()
		if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, spelling)); err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if got, _ := cmd.Flags().GetString("host"); got != "u@m" {
			t.Errorf("%q set host to %q", spelling, got)
		}
	}
	cmd := RootCmd()
	if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, []string{"-remove", "-name", "n", "o/r"})); err != nil {
		t.Fatal(err)
	}
	if r, _ := cmd.Flags().GetBool("remove"); !r || cmd.Flags().NArg() != 1 {
		t.Errorf("remove %v, operands %q", r, cmd.Flags().Args())
	}
}

func TestMisuseIsStatusTwo(t *testing.T) {
	for _, args := range [][]string{nil, {"-host", "u@m"}, {"-host", "u@m", "norepo"}, {"-nope"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 || strings.TrimSpace(errb.String()) == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errb.String())
		}
	}
}
