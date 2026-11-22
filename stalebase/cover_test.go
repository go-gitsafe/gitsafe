package stalebase

import (
	"reflect"
	"testing"
)

// Plumbing is not an operand: `--`, flags, redirections and expansions are
// skipped, and only a plain word is kept.
func TestOperandsSkipPlumbing(t *testing.T) {
	got := operands([]string{"--", "-b", `"$X"`, "'q'", "$V", "`c`", "A=b", ">", "f", "2>/dev/null", "plain"})
	if want := []string{"plain"}; !reflect.DeepEqual(got, want) {
		t.Errorf("operands = %q, want %q", got, want)
	}
}

// git with only options names no subcommand.
func TestSubcommandOfOnlyOptionsIsEmpty(t *testing.T) {
	if sub, rest := subcommand([]string{"-C", "/r", "--no-pager"}); sub != "" || rest != nil {
		t.Errorf("subcommand = %q, %q", sub, rest)
	}
}

// A path to git reads as git.
func TestAPathToGitIsGit(t *testing.T) {
	args, ok := gitArgs("/usr/bin/git branch x")
	if !ok || len(args) != 2 {
		t.Errorf("gitArgs = %q, %v", args, ok)
	}
}
