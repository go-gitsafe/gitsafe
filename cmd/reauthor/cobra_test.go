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
			t.Errorf("reauthor %s exited %d\nstderr: %s", arg, code, errb.String())
		}
		for _, want := range []string{"--identities", "--to", "--dry-run", "--scan"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("reauthor %s does not name %s:\n%s", arg, want, out.String())
			}
		}
	}
}

// A value that looks like a flag is a value: `-to -identities` is the literal
// string, and the adapter must not rewrite it.
func TestBothSpellingsOfEveryFlag(t *testing.T) {
	for _, spelling := range [][]string{
		{"-identities", "ids"}, {"--identities", "ids"}, {"-identities=ids"}, {"--identities=ids"},
	} {
		cmd := RootCmd()
		if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, spelling)); err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if got, _ := cmd.Flags().GetString("identities"); got != "ids" {
			t.Errorf("%q set identities to %q", spelling, got)
		}
	}
	cmd := RootCmd()
	if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, []string{"-dry-run", "-to", "-identities", "repo"})); err != nil {
		t.Fatal(err)
	}
	if to, _ := cmd.Flags().GetString("to"); to != "-identities" {
		t.Errorf("to = %q, want the literal -identities", to)
	}
	if d, _ := cmd.Flags().GetBool("dry-run"); !d || cmd.Flags().NArg() != 1 {
		t.Errorf("dry-run %v, operands %q", d, cmd.Flags().Args())
	}
}
