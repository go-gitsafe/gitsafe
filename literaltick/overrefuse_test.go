package literaltick

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ THE OTHER DIRECTION OF THE ORACLE, AND THE ONE THAT WAS MISSING.
//
// [TestTheRuleRefusesWhateverARealShellRuns] asks "did a shell RUN this
// backtick?" and requires a refusal. That direction can only ever find a MISS.
// An over-refusal — a command no shell would evaluate, refused anyway — is
// invisible to it, and a guard is abandoned for over-refusing long before it is
// abandoned for missing something.
//
// Measured with guardscan over this machine's own transcripts, the rule as
// released refused 5 050 of 554 998 commands, and the top of that table was one
// shape: a PR body or commit message passed as `"$(cat <<'EOF' … EOF\n)"` with
// an ordinary pair of parentheses somewhere in the prose. That is the form this
// package's own Advice prescribes.
//
// So this test states the converse rule: if NO shell runs the backtick, the
// guard must not refuse. The shells include zsh, which is the one that executes
// the commands on this machine (see #121 for the forward oracle, which still
// asks only bash and dash).
func TestTheRuleAllowsWhatNoRealShellRuns(t *testing.T) {
	var shells []string
	for _, s := range []string{"zsh", "bash", "dash"} {
		if p, err := exec.LookPath(s); err == nil {
			shells = append(shells, p)
		} else {
			t.Logf("no %s here: its half of the oracle is skipped", s)
		}
	}
	if len(shells) == 0 {
		t.Skip("no shell to ask")
	}

	// Every case holds T, so every case is observable: if the marker file is
	// absent after running it, that shell did not evaluate the backtick.
	cases := []struct{ name, cmd string }{
		{
			// The shape at the top of the guardscan table.
			"a PR body with a parenthesis in the prose",
			"echo --body \"$(cat <<'EOF'\n- hosts run freely (unlike the default " + T + ") by design\nEOF\n)\"\n",
		},
		{
			"a commit message with a parenthesis in the prose",
			"git commit -m \"$(cat <<'EOF'\nfix: do it once (not " + T + " times)\nEOF\n)\"\n",
		},
		{
			// Without the parenthesis the same body is already allowed; it is
			// here so a fix that breaks the working half is caught too.
			"the same body with no parenthesis",
			"echo --body \"$(cat <<'EOF'\n- hosts run freely, unlike " + T + "\nEOF\n)\"\n",
		},
		{
			"a quoted heredoc on its own, with a parenthesis",
			"cat <<'EOF'\nprose (with " + T + " in it)\nEOF\n",
		},
		{
			"two parentheses in the body",
			"echo \"$(cat <<'EOF'\n(a) and (b) and " + T + "\nEOF\n)\"\n",
		},
		// ⛔ THE OTHER SIDE, AND THE FUZZ CORPUS FOUND IT. A ) in the body with
		// no ( of its own really can end the substitution in bash: the committed
		// seed "$(<<'E'\ncase x in x) `:>ran`E\nE\ncase x in x) " ran. These
		// cases are here so the two readings are told apart by the shells rather
		// than by me -- whichever way a shell answers, the loop above checks the
		// rule against that answer.
		{
			"an unbalanced ) in the body",
			"echo \"$(cat <<'EOF'\nx) " + T + "\nEOF\n)\"\n",
		},
		{
			"a case pattern in the body",
			"echo \"$(cat <<'EOF'\ncase x in x) " + T + "\nEOF\n)\"\n",
		},
		{
			"the fuzz seed itself",
			"\"$(<<'E'\ncase x in x) " + T + "E\nE\ncase x in x) \"",
		},
	}

	for _, tc := range cases {
		anyRan := false
		for _, sh := range shells {
			if runs(t, sh, tc.cmd) {
				anyRan = true
				t.Logf("%s RAN the backtick of %q: this case belongs to the forward oracle",
					filepath.Base(sh), tc.name)
			}
		}
		if anyRan {
			// A shell really does evaluate it: refusing is then correct, and the
			// forward oracle owns the case.
			if !Check(tc.cmd).Found() {
				t.Errorf("%s: a shell ran it and the rule allowed it", tc.name)
			}
			continue
		}
		if f := Check(tc.cmd); f.Found() {
			t.Errorf("%s: NO shell (%s) evaluated the backtick, and the rule refused anyway\n  matched: %s\n  command: %s",
				tc.name, strings.Join(base(shells), ", "), f.Context, tc.cmd)
		}
	}
}

func base(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}
