package plain

import "testing"

// Each construct the grammar cannot place is Uncertain, with its own reason, in
// the contexts where it can hide: a heredoc waiting for its body while a quote
// is open, `$((` inside double quotes, `>(`, a `)` with a heredoc pending and a
// heredoc that never gets a body.
func TestUncertainReasonsInTheCornersOfTheGrammar(t *testing.T) {
	for _, tc := range []struct{ cmd, reason string }{
		{"cat <<'E' \"a\nb\" `x`\nE", "newline in quotes with a heredoc pending"},
		{"cat <<'E' 'a\nb' `x`\nE", "newline in quotes with a heredoc pending"},
		{"echo \"$((1)) `x`\"", "$(("},
		{"echo >(echo `x`)", ">("},
		{"echo $(cat <<'E' `x`)\nE", ") with a heredoc pending"},
		{"cat <<'E' `x`", "heredoc with no body"},
	} {
		got := check(tc.cmd, true)
		if got.V != Uncertain || got.Reason != tc.reason {
			t.Errorf("%q = %v %q, want Uncertain %q", tc.cmd, got.V, got.Reason, tc.reason)
		}
	}
}
