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
		// #128. Each ALLOW ran nothing under bash 3.2.57, dash and zsh -f -c; the
		// refusal beside it is the nearest shape that does (zsh reads a glob).
		{"#128 local array", "local a=(1 2)", true},
		{"#128 declare with a flag", "declare -a a=(1 2)", true},
		{"#128 export array", "export a=(1 2)", true},
		{"#128 typeset and readonly", "typeset -r a=(1)", true},
		{"#128 an assignment prefix", "FOO=1 a=(1)", true},
		{"#128 two assignment prefixes", "FOO=1 BAR=2 a=(1)", true},
		{"#128 echo array stays unsure (zsh glob)", "echo a=(1 2)", false},
		{"#128 a flag after a non-builtin", "echo -a a=(1)", false},
		{"#128 a builtin that is an argument", "echo local a=(1)", false},
		{"#128 a command between prefix and array", "FOO=1 echo a=(1)", false},
		{"#128 glued flag-less word", "locala=(1)", true},
		{"#128 case arm subshell", "case x in a) (cd y);; esac", true},
		{"#128 later case arm", "case x in a) :;; b) (cd y);; esac", true},
		{"#128 alternative patterns", "case x in a|b) (cd y);; esac", true},
		{"#128 an argument before a ) is not a case arm", "echo a) (x)", false},
		{"#128 a subshell closer is not a case arm", "(a) (x)", false},
		{"#128 an argument glob after a case arm word", "case x in a) echo (x);; esac", false},
		{"#128 after a heredoc terminator", "cat <<'E'\nfoo\nE\n(cd /)", true},
		{"#128 after an unquoted heredoc terminator", "cat <<E\nfoo\nE\n(cd /)", true},
		{"#128 after a heredoc's last body line", "cat <<'E'\nfoo (x)", false},
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
