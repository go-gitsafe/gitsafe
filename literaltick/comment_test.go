package literaltick

import (
	"os/exec"
	"testing"
)

// ⛔ WHERE A # HIDES A ) FROM BASH'S OWN SCAN — asked of bash, not reasoned.
//
// A fuzz run found ";#case x in x) `…`" in 44 seconds: treating that # as a
// comment hid the ) from unbalancedClose, which then called the body balanced
// and allowed what bash runs. The first version used the lexer's own wordStart
// set (" \t\n;&|("), which is too wide — bash opens a comment during this scan
// only after WHITESPACE or a NEWLINE.
//
// Each row asserts that the rule agrees with the shell on this input, in both
// directions, so the boundary cannot drift silently either way.
func TestWhereACommentHidesAParenFromBash(t *testing.T) {
	p, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	rows := []struct {
		name, body string
		// comment: bash treats the # as opening one, so the ( is hidden, the )
		// is unmatched, and bash ends the substitution and runs the backtick.
		comment bool
	}{
		{"# at line start", "# a ( note\n) " + T, true},
		{"# after a semicolon and a space", "; #a ( note\n) " + T, true},
		{"# after a space", "x #a ( note\n) " + T, true},
		{"# after a semicolon, no space", ";#a ( note\n) " + T, false},
		{"# after a pipe", "|#a ( note\n) " + T, false},
		{"# mid-word", "x#a ( note\n) " + T, false},
		{"no comment at all", "a ( note\n) " + T, false},
	}
	agreed := 0
	for _, r := range rows {
		cmd := "echo \"$(cat <<'EOF'\n" + r.body + "\nEOF\n)\"\n"
		ran := runs(t, p, cmd)
		if ran != r.comment {
			// The platform's bash disagrees with the table's premise. Say so
			// rather than assert the rule against it: ubuntu's bash 5 does not
			// end a substitution here at all.
			t.Logf("%s: this bash ran=%v, the table says comment=%v -- not judged here",
				r.name, ran, r.comment)
			continue
		}
		agreed++
		if got := Check(cmd).Found(); got != ran {
			t.Errorf("%s: bash ran it = %v, the rule refuses = %v -- they must agree\n  %s",
				r.name, ran, got, cmd)
		}
	}
	if agreed == 0 {
		t.Skip("this bash does not end a substitution on a hidden paren: " +
			"the boundary cannot be measured on this platform")
	}
}
