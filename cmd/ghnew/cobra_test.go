package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

// run is the shape the older tests were written against: arguments in, the
// error out. It drives the very command a person gets, through the adapter.
func run(args []string, out io.Writer) error {
	var errb bytes.Buffer
	if code := execute(args, out, &errb); code != 0 {
		return errors.New(strings.TrimSpace(errb.String()))
	}
	return nil
}

func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help", "help"} {
		var out, errb bytes.Buffer
		cmd := RootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if code := cliflags.Run(cmd, []string{arg}); code != 0 {
			t.Errorf("ghnew %s exited %d\nstderr: %s", arg, code, errb.String())
		}
		for _, want := range []string{"--public", "--private", "--license", "--gitignore", "--branch", "--clone", "--token-file"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("ghnew %s does not name %s:\n%s", arg, want, out.String())
			}
		}
	}
}

func TestBothSpellingsOfEveryFlag(t *testing.T) {
	for _, spelling := range [][]string{
		{"-token-file", "t"}, {"--token-file", "t"}, {"-token-file=t"}, {"--token-file=t"},
	} {
		cmd := RootCmd()
		if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, spelling)); err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if got, _ := cmd.Flags().GetString("token-file"); got != "t" {
			t.Errorf("%q set token-file to %q", spelling, got)
		}
	}
}

// A misuse is a status of its own, and says what to type.
func TestWrongOperandCountIsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := execute([]string{"-public"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "usage: ghnew") {
		t.Errorf("exit %d, %q", code, errb.String())
	}
}
