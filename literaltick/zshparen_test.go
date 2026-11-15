package literaltick

import "testing"

// ⛔ #128: shapes the zsh ( rule refused although no shell ran anything. Every
// row DECLARES the verdict; each one ALLOWED is also put to bash, dash and
// zsh -f -c, and none may run the backtick. The refused rows are the nearest
// shapes that do run it (or that zsh reads as a glob), so a narrowing that is
// too wide fails here and not in somebody's pull request.
func TestParenShapesThatRunNothing(t *testing.T) {
	q := "echo '" + T + "'"
	shells := oracleShells(t)
	for _, tc := range []struct {
		name, cmd string
		refuse    bool
	}{
		{"local array", "local a=(1 2); " + q, false},
		{"declare -a", "declare -a a=(1 2); " + q, false},
		{"export array", "export a=(1 2); " + q, false},
		{"typeset array", "typeset a=(1 2); " + q, false},
		{"readonly array", "readonly a=(1 2); " + q, false},
		{"assignment prefix", "FOO=1 a=(1) " + q, false},
		{"array with a comment inside", "local a=(1 #" + T + ")\n" + q, false},
		{"array with a newline comment", "export a=(1\n#" + T + "\n)\n" + q, false},
		{"case arm subshell", "case x in a) (cd y);; esac; " + q, false},
		{"case arm subshell, comment inside", "case x in a) (cd y\n#" + T + ");; esac; " + q, false},
		{"after a quoted heredoc terminator", "cat <<'E'\nfoo\nE\n(cd /); " + q, false},
		{"after a heredoc terminator, comment inside", "cat <<'E'\nfoo\nE\n(cd /\n#" + T + ")", false},
		// The nearest shapes that DO run, or that zsh reads as a glob.
		{"a live backtick in a local array", "local a=(1 " + T + ")", true},
		{"echo array with a # (zsh glob)", "echo a=(#" + T + ")", true},
		{"echo array, newline #", "echo a=(1\n#" + T + ")", true},
		{"a # right after the ( of a local array", "local a=(#" + T + ")", true},
		{"a | # after the ( of a local array", "local a=(|#" + T + ")", true},
		{"echo glob in a case arm", "case x in a) echo (#" + T + ");; esac", true},
		{"after a heredoc's terminator, ( then #", "cat <<'E'\nfoo\nE\n(#" + T + ")", true},
		{"a [[ =~ group with a live backtick", "[[ a =~ (b|#" + T + ") ]]", true},
		{"a zsh for list", "for x in (a|#" + T + "); do :; done", true},
	} {
		if got := check(tc.cmd).Found(); got != tc.refuse {
			t.Errorf("%s: the rule refuses = %v, declared %v\n  %q", tc.name, got, tc.refuse, tc.cmd)
		}
		if !tc.refuse {
			for _, sh := range shells {
				if runs(t, sh, tc.cmd) {
					t.Errorf("%s: %s RAN the backtick of an ALLOWED command\n  %q", tc.name, sh, tc.cmd)
				}
			}
		}
	}
}
