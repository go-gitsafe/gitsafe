package plain

import (
	"strings"
	"testing"
)

// ⛔ EACH REFUSAL, ISOLATED (#156). A refusal that no test can tell from the
// backtick rule that follows it can be deleted silently: with the unquoted-# and
// bare-( refusals turned into no-ops, the corpus test stayed green, because every
// input in it also carries a backtick that is evaluated and refused anyway.
//
// So each row names the refusal, and the test switches THAT refusal off
// (checkSkipping) and requires the grammar to ALLOW the row without it: the
// refusal is then the only thing between the command and the shell. The real
// production zsh is asked whether the row's backtick runs, and the answer is
// stated per row rather than assumed.
//
// Honest limit: where `ran` is false the refusal is conservative, not necessary —
// no zsh run here executed the backtick. Only the unquoted # has a row the
// production zsh really runs (found by FuzzSplus' generator). Quote parity makes
// the others hard: a ' that plain pairs across a line and zsh does not is odd on
// one side and leaves zsh a parse error, so nothing runs at all.
func TestEachRefusalIsIsolatedFromTheBacktickRule(t *testing.T) {
	zshPath(t)
	for _, tc := range []struct {
		skip     string // prefix of the reason the refusal names
		cmd      string // refused for that reason
		isolated bool   // allowed by the grammar when that refusal is off
		ran      bool   // the production zsh runs the backtick
		twin     string // the nearest command the grammar allows
	}{
		{"unquoted #", "E\n#<<-'E'\n*" + T + "}a<<-E\nE\n", true, true, "echo '# " + T + "'"},
		{"bare (", "echo (x '\n" + T + "'", true, false, "echo '(x' '" + T + "'"},
		{"${", "echo ${x:-'" + T + "'}", true, false, "echo '${x:-" + T + "}'"},
		{"case", "echo esac '" + T + "'", true, false, "echo 'esac' '" + T + "'"},
		{"$((", "echo $(('" + T + "'))", false, true, "echo '$((" + T + "))'"},
		{"$'", "echo $'a\\'" + T + "'", false, false, "echo 'a$'" + " '" + T + "'"},
		{"<< unquoted", "cat <<E\n" + T + "\nE\n", false, true, "cat <<'E'\n" + T + "\nE\n"},
	} {
		got := CheckHeredoc(tc.cmd)
		if got.V != Uncertain || !strings.HasPrefix(got.Reason, tc.skip) {
			t.Errorf("%q: CheckHeredoc = %v %q, want Uncertain naming %q\n  %q", tc.skip, got.V, got.Reason, tc.skip, tc.cmd)
		}
		if tc.isolated {
			if v := checkSkipping(tc.cmd, true, tc.skip).V; v != Allow {
				t.Errorf("%q: with that refusal off the grammar says %v, want Allow: the row is not isolated\n  %q", tc.skip, v, tc.cmd)
			}
		}
		if ran := runs(t, "zsh", tc.cmd); ran != tc.ran {
			t.Errorf("%q: the production zsh ran the backtick = %v, row says %v\n  %q", tc.skip, ran, tc.ran, tc.cmd)
		}
		if v := CheckHeredoc(tc.twin).V; v != Allow {
			t.Errorf("%q: the twin is %v, want Allow\n  %q", tc.skip, v, tc.twin)
		}
		if runs(t, "zsh", tc.twin) {
			t.Errorf("%q: the production zsh ran the allowed twin: %q", tc.skip, tc.twin)
		}
	}
}
