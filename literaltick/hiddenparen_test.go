package literaltick

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ A ( BASH NEVER SEES, AND THE FALSE ALLOW IT CAUSED.
//
// bash finds the ) that closes a $( … ) by scanning the characters, and that
// scan skips quoted spans and comments. The first version of unbalancedClose
// counted a ( everywhere, so a ( hidden inside a span made the following )
// look matched — while bash, not having seen the (, took that ) as the end of
// the substitution and ran everything after it, this package having declared
// it a quoted heredoc body.
//
// ⛔ EVERY INPUT BELOW IS ORDINARY PROSE, and four of the five are refused by
// bash 3.2.57 here. They were found by an independent review of the pull
// request, not by this package's own tests: every row of
// [TestTheRuleAllowsWhatNoRealShellRuns] happened to be quote-free, so the
// population omitted the breaking shape entirely. A table can only be as
// honest as its own variety.
func TestAParenHiddenInASpanIsNotCounted(t *testing.T) {
	var shells []string
	for _, s := range []string{"zsh", "bash", "dash"} {
		if p, err := exec.LookPath(s); err == nil {
			shells = append(shells, p)
		}
	}
	if len(shells) == 0 {
		t.Skip("no shell to ask")
	}

	cases := []struct{ name, cmd string }{
		{
			"an apostrophe before the open paren",
			"gh pr create --title t --body \"$(cat <<'EOF'\ndon't do this (it's wrong) and run " + T + " instead\nEOF\n)\"\n",
		},
		{
			"a fenced command in the prose",
			"gh pr create --body \"$(cat <<'EOF'\n- don't use `pkill -f` (it's a trap), use " + T + "\nEOF\n)\"\n",
		},
		{
			"a single-quoted paren",
			"echo \"$(cat <<'EOF'\nsee '(' ) " + T + "\nEOF\n)\"\n",
		},
		{
			"a paren inside a comment in the body",
			"echo \"$(cat <<'EOF'\n# a ( note\n) " + T + "\nEOF\n)\"\n",
		},
		{
			"a double-quoted paren",
			"echo \"$(cat <<'EOF'\nsee \\\"(\\\" ) " + T + "\nEOF\n)\"\n",
		},
	}

	ran := 0
	for _, tc := range cases {
		var ranOn []string
		for _, sh := range shells {
			if runs(t, sh, tc.cmd) {
				ranOn = append(ranOn, filepath.Base(sh))
				ran++
			}
		}
		if len(ranOn) > 0 && !Check(tc.cmd).Found() {
			t.Errorf("FALSE ALLOW -- %s: %s evaluated the backtick and the rule allowed it\n  %s",
				tc.name, strings.Join(ranOn, ", "), tc.cmd)
		}
	}

	// ⛔ The control: if no shell evaluated any of these, the table proves
	// nothing and must not read as a pass. On a platform whose bash does not
	// end the substitution here (ubuntu's bash 5 does not), that is a SKIP
	// rather than a green, because the inputs were chosen for the shell that
	// does.
	if ran == 0 {
		t.Skip("no shell here ends a substitution on a hidden paren: " +
			"these inputs cannot be judged on this platform")
	}
}
