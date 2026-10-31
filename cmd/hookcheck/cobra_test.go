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
			t.Errorf("hookcheck %s exited %d\nstderr: %s", arg, code, errb.String())
		}
		for _, want := range []string{"--install", "--settings"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("hookcheck %s does not name %s:\n%s", arg, want, out.String())
			}
		}
	}
}

// -install is the documented spelling and is single-dash: `hookcheck -install`
// writes the settings file, so reading it as the cluster i,n,s,t,a,l,l would be
// the worst place for the trap.
func TestBothSpellingsOfEveryFlag(t *testing.T) {
	for _, spelling := range [][]string{
		{"-settings", "s.json"}, {"--settings", "s.json"}, {"-settings=s.json"}, {"--settings=s.json"},
	} {
		cmd := RootCmd()
		if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, spelling)); err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if got, _ := cmd.Flags().GetString("settings"); got != "s.json" {
			t.Errorf("%q set settings to %q", spelling, got)
		}
	}
	cmd := RootCmd()
	if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, []string{"-install"})); err != nil {
		t.Fatal(err)
	}
	if got, _ := cmd.Flags().GetBool("install"); !got {
		t.Error("-install was not read as the flag")
	}
}

func TestAnUnknownFlagIsStatusTwo(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-nope"}, &out, &errb); code != 2 {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}
