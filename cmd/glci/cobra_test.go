package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

// All four spellings of the question, through the command a person gets.
func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help", "help"} {
		var out, errb bytes.Buffer
		cmd := RootCmd(t.TempDir())
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if code := cliflags.Run(cmd, []string{arg}); code != 0 {
			t.Errorf("glci %s exited %d\nstderr: %s", arg, code, errb.String())
		}
		for _, want := range []string{"--host", "--project", "--watch", "--every", "--log", "--file"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("glci %s does not name %s:\n%s", arg, want, out.String())
			}
		}
	}
}

// -f is declared, so pflag reads `-file x` as -f with the value "ile" and leaves
// x behind as a ref. Both halves: the adapter, and the control.
func TestTheSingleDashFileSpellingIsNotTheShorthand(t *testing.T) {
	cmd := RootCmd(t.TempDir())
	if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, []string{"-file", "/tmp/tok"})); err != nil {
		t.Fatal(err)
	}
	if got, _ := cmd.Flags().GetString("file"); got != "/tmp/tok" || cmd.Flags().NArg() != 0 {
		t.Errorf("file = %q, operands %q", got, cmd.Flags().Args())
	}
	bare := RootCmd(t.TempDir())
	if err := bare.Flags().Parse([]string{"-file", "/tmp/tok"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := bare.Flags().GetString("file"); got != "ile" || bare.Flags().NArg() != 1 {
		t.Errorf("control: file = %q, operands %q; want \"ile\" and one operand", got, bare.Flags().Args())
	}
}

// The spellings this tool's documentation uses are single-dash.
func TestBothSpellingsOfEveryFlag(t *testing.T) {
	for _, spelling := range [][]string{
		{"-host", "gl.example"}, {"--host", "gl.example"}, {"-host=gl.example"}, {"--host=gl.example"},
	} {
		cmd := RootCmd(t.TempDir())
		if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, spelling)); err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if got, _ := cmd.Flags().GetString("host"); got != "gl.example" {
			t.Errorf("%q set host to %q", spelling, got)
		}
	}
	cmd := RootCmd(t.TempDir())
	if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, []string{"-watch", "-every", "5s", "-log", "0", "-project", "a/b", "main"})); err != nil {
		t.Fatal(err)
	}
	if w, _ := cmd.Flags().GetBool("watch"); !w || cmd.Flags().NArg() != 1 {
		t.Errorf("-watch not set or operands wrong: %q", cmd.Flags().Args())
	}
}
