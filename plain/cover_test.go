package plain

import "testing"

// startsComment on both sides of each guard, asserted by value; a panic is
// reported as a failure of the row, not left to end the test binary.
func TestStartsCommentByValue(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		i    int
		want bool
	}{
		{"#x", 0, true},
		{"a #x", 2, true},
		{"a\t#x", 2, true},
		{"a\n#x", 2, true},
		{"a#x", 1, false},
		{"a\\ #x", 3, false},
		{"a\\\n#x", 3, false},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("startsComment(%q, %d) panicked: %v", tc.cmd, tc.i, r)
				}
			}()
			if got := startsComment(tc.cmd, tc.i); got != tc.want {
				t.Errorf("startsComment(%q, %d) = %v, want %v", tc.cmd, tc.i, got, tc.want)
			}
		}()
	}
}

// With the $' refusal switched off the scan goes on as if the $ were ordinary, so
// the bare backtick after the quote is what the grammar then refuses.
func TestASkippedAnsiCRefusalLeavesTheScanRunning(t *testing.T) {
	if got := checkSkipping("echo $'\\cx' "+T, true, "$'").V; got != Refuse {
		t.Errorf("with the $' refusal off: %v, want Refuse", got)
	}
}

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
