package literaltick

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ $'…' HONOURS A BACKSLASH AND '…' DOES NOT, and the difference decides
// where a span ends — so it decides which ) is unquoted.
//
// bash reads this as ONE span, because a backslash inside $'…' escapes:
//
//	$'a\''
//
// while this is TWO, because a backslash is literal inside single quotes:
//
//	'a\''
//
// ⛔ The literals above are in code blocks on purpose. gofmt reformats a doc
// comment's PROSE, and two apostrophes in a row there become one typographic
// quote — in a comment about shell quoting, that makes it state something
// else. It happened to this very comment, twice, including to the sentence
// warning about it; a code block is the only place gofmt leaves them alone.
//
// unbalancedClose skipped both alike, closing the first early at the \' and
// then reading the trailing ' as an unterminated span —
// which [unbalancedClose] treats as an allow. bash 3.2.57 ends the
// substitution at the ) after it and runs what follows: a FALSE ALLOW, and a
// regression of the fix for the hidden-paren one, found by the second pass of
// the same review.
//
// The row without the $ is the control that isolates the cause: no shell runs
// it, and the rule must not refuse it.
func TestAnsiCQuotingEndsWhereBashEndsIt(t *testing.T) {
	var shells []string
	for _, s := range []string{"zsh", "bash", "dash"} {
		if p, err := exec.LookPath(s); err == nil {
			shells = append(shells, p)
		}
	}
	if len(shells) == 0 {
		t.Skip("no shell to ask")
	}

	body := func(line string) string {
		return "echo --body \"$(cat <<'EOF'\n" + line + "\nEOF\n)\"\n"
	}
	cases := []struct{ name, cmd string }{
		{"$'a\\'' then a paren", body(`$'a\'' ) ` + T)},
		{"$'(\\'' then a paren", body(`$'(\'' ) ` + T)},
		{"two of them", body(`$'a\'' $'b\'' ) ` + T)},
		{"a terminated tail after it", body(`$'a\'' ) ` + T + ` and 'more'`)},
		// ⛔ This one is this repository's OWN reviewCases row pasted into a PR
		// body, which is how it reaches a real command here.
		{"the repo's own case in prose", body(`run echo $'x\'' first, then ) ` + T)},
		{"a double-quoted paren", body(`see "(" ) ` + T)},
		// The control: without the $, a backslash is literal, the span ends at
		// the first ', and no shell ends the substitution.
		{"plain '…' with a backslash", body(`'a\'' ) ` + T)},
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
		refused := Check(tc.cmd).Found()
		if len(ranOn) > 0 && !refused {
			t.Errorf("FALSE ALLOW -- %s: %s evaluated the backtick and the rule allowed it\n  %s",
				tc.name, strings.Join(ranOn, ", "), tc.cmd)
		}
		if len(ranOn) == 0 && refused {
			t.Logf("%s: no shell ran it and the rule refuses it (over-refusal, not a hole)", tc.name)
		}
	}
	if ran == 0 {
		t.Skip("no shell here ends a substitution on these: not judgeable on this platform")
	}
}
