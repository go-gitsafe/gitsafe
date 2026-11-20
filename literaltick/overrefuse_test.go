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
// # ⛔ WHY EACH CASE DECLARES ITS OWN EXPECTATION
//
// The first version of this test inferred the expectation from the shells:
// "no shell here ran it, so the rule must allow". That inference is wrong, and
// CI said so within three minutes of the first push. A ) in a quoted heredoc
// body inside $( … ) ends the substitution in the bash on this machine —
// 3.2.57, which is the bash Apple ships — and does NOT end it in the bash on
// ubuntu-latest. The same three cases therefore ran here and did nothing there.
//
// An absence of evidence on the shells that happen to be installed is not
// evidence that no shell evaluates it, and a guard must refuse what ANY shell
// would run. So the table states what the rule must do, and the shells are used
// to check that statement rather than to make it:
//
//   - allow: no installed shell may evaluate it. If one does, the DECLARATION is
//     wrong and the test says so — that is the case that would be a miss.
//   - refuse: at least one shell somewhere evaluates it. A shell that does is
//     corroboration; one that does not proves nothing, as bash 5 shows.
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
	cases := []struct {
		name  string
		cmd   string
		allow bool
	}{
		{
			// The shape at the top of the guardscan table.
			name:  "a PR body with a parenthesis in the prose",
			cmd:   "echo --body \"$(cat <<'EOF'\n- hosts run freely (unlike the default " + T + ") by design\nEOF\n)\"\n",
			allow: true,
		},
		{
			name:  "a commit message with a parenthesis in the prose",
			cmd:   "git commit -m \"$(cat <<'EOF'\nfix: do it once (not " + T + " times)\nEOF\n)\"\n",
			allow: true,
		},
		{
			// Without the parenthesis the same body was already allowed; it is
			// here so a fix that breaks the working half is caught too.
			name:  "the same body with no parenthesis",
			cmd:   "echo --body \"$(cat <<'EOF'\n- hosts run freely, unlike " + T + "\nEOF\n)\"\n",
			allow: true,
		},
		{
			name:  "a quoted heredoc on its own, with a parenthesis",
			cmd:   "cat <<'EOF'\nprose (with " + T + " in it)\nEOF\n",
			allow: true,
		},
		{
			name:  "two parentheses in the body",
			cmd:   "echo \"$(cat <<'EOF'\n(a) and (b) and " + T + "\nEOF\n)\"\n",
			allow: true,
		},
		// ⛔ PRUNED (#156): three rows here declared a ) with no ( of its own in a
		// quoted body inside $( ) must be refused, because bash 3.2.57 ends the
		// substitution there. That was a bash-only reading; commands run under zsh,
		// and the bash-3.2 inputs are refused by name under the plain grammars
		// (TestCheckForRefusesWhateverTheShellOfItsGrammarRuns).
		// ⛔ THE CONTROL. A bare backtick every shell evaluates, so a run in
		// which NOTHING is observed is reported as a blind oracle rather than a
		// clean one.
		{
			name:  "a bare backtick, which every shell runs",
			cmd:   "echo " + T + "\n",
			allow: false,
		},
	}

	observed := 0
	for _, tc := range cases {
		var ranOn []string
		for _, sh := range shells {
			if runs(t, sh, tc.cmd) {
				ranOn = append(ranOn, filepath.Base(sh))
				observed++
			}
		}
		refused := Check(tc.cmd).Found()

		if tc.allow {
			if len(ranOn) > 0 {
				t.Errorf("%s: declared allowable, but %s EVALUATED the backtick: "+
					"the declaration is wrong, not the rule\n  command: %s",
					tc.name, strings.Join(ranOn, ", "), tc.cmd)
			}
			if refused {
				t.Errorf("%s: no shell of %s evaluated the backtick, and the rule refused anyway\n  matched: %s\n  command: %s",
					tc.name, strings.Join(base(shells), ", "), Check(tc.cmd).Context, tc.cmd)
			}
			continue
		}
		if !refused {
			t.Errorf("%s: must be refused, and was allowed\n  command: %s", tc.name, tc.cmd)
		}
		if len(ranOn) == 0 {
			// Not a failure: bash 5 does not run what bash 3.2 runs.
			t.Logf("%s: no shell here evaluated it; the refusal rests on the "+
				"shells that do, not on this platform", tc.name)
			continue
		}
		t.Logf("%s: %s evaluated it, as the refusal expects", tc.name, strings.Join(ranOn, ", "))
	}

	// ⛔ A SCAN THAT COULD NOT READ MUST NOT REPORT A CLEAN RESULT. The bare
	// backtick above runs under every shell there is; if nothing at all was
	// observed, the marker, the temporary directory or the PATH is broken and
	// every "no shell ran it" above is meaningless.
	if observed == 0 {
		t.Fatalf("no shell evaluated any backtick, the bare one included: "+
			"the oracle cannot see what it is for (shells: %s)",
			strings.Join(base(shells), ", "))
	}
}

func base(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}
