package shellcmd

import (
	"strings"
	"testing"
)

// TestParenIsShell pins which ( the classifier trusts (#121): the ones every
// shell reads as syntax. Each case marks the ( under test with the LAST ( of
// the input is the FIRST ( of the input.
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
		{"after a newline", "a\n(x)", true},
		{"after an assignment", "a=(1 2)", true},
		{"a process substitution", "cat <(x)", true},
		{"after the keyword in", "case x in (x) :;; esac", true},
		{"after the keyword then", "if a; then (x); fi", true},
		{"a keyword inside a word is not a keyword", "echo asin (x)", false},
		{"a keyword glued to a hyphen is part of a word", "echo a-in (x)", false},
		{"a keyword at the very start", "do (x)", true},
		{"after an argument", ": (x)", false},
		{"glued to an argument", "echo a(x)", false},
		{"after a quoted argument", "echo 'a' (x)", false},
		{"after a bare word that is no keyword", "echo (x)", false},
	} {
		in := c.in
		i := strings.IndexByte(in, '(')
		if i < 0 {
			t.Fatalf("%s: no ( in %q", c.name, in)
		}
		if got := parenIsShell(in, Classify(in), i); got != c.want {
			t.Errorf("%s: parenIsShell(%q, %d) = %v, want %v", c.name, in, i, got, c.want)
		}
	}
}
