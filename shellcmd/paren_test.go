package shellcmd

import (
	"strings"
	"testing"
)

// TestParenIsShell pins which ( the classifier trusts (#121): the ones every
// shell reads as syntax. The ( under test is the LAST ( of the input.
func TestParenIsShell(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want bool
	}{
		{"first byte", "(x)", true},
		{"after spaces only", "  (x)", true},
		{"after a semicolon", "a; (x)", true},
		{"after an ampersand", "a & (x)", true},
		{"after &&", "a && (x)", true},
		{"after a redirection's &", "echo >& (x)", false},
		{"after a newline", "a\n(x)", true},
		{"after a pipe", "a | (x)", true},
		{"an assignment in command position", "a=(1 2)", true},
		{"an appending assignment", "a+=(1 2)", true},
		{"an assignment that is an argument", "echo a=(x)", false},
		{"an assignment with no name", "=(x)", false},
		{"a process substitution", "cat <(x)", true},
		{"after the keyword in of a case", "case x in (x) :;; esac", true},
		{"in after a case with a quoted subject", "case \"$x\" in (x) :;; esac", false},
		{"in that is an argument", "echo in (x)", false},
		{"in glued to its subject", "case x in(x) :;; esac", false},
		{"in after a word that is not case", "for x in (a b)", false},
		{"in after case that is an argument", "echo case x in (x)", false},
		{"after the keyword then", "if a; then (x); fi", true},
		{"a keyword that is an argument", "echo then (x)", false},
		{"a keyword glued to the paren", "if(x)", false},
		{"a keyword glued to a hyphen is part of a word", "echo a-in (x)", false},
		{"a keyword at the very start", "do (x)", true},
		{"after !", "! (x)", true},
		{"after [[", "[[ (x) ]]", true},
		{"after coproc", "coproc (x)", true},
		{"a brace group", "{ (x); }", true},
		{"a brace glued to the paren", "{(x)", false},
		{"a brace inside a word", "echo a{ (x)", false},
		{"after a ) ", "echo $(true) (x)", false},
		{"glued to a ) ", "echo $(true)(x)", false},
		{"after an argument", ": (x)", false},
		{"glued to an argument", "echo a(x)", false},
		{"after a quoted argument", "echo 'a' (x)", false},
		{"after an escaped semicolon", "echo \\; (x)", false},
	} {
		in := c.in
		i := strings.LastIndexByte(in, '(')
		if i < 0 {
			t.Fatalf("%s: no ( in %q", c.name, in)
		}
		if got := parenIsShell(in, Classify(in), i); got != c.want {
			t.Errorf("%s: parenIsShell(%q, %d) = %v, want %v", c.name, in, i, got, c.want)
		}
	}
}
