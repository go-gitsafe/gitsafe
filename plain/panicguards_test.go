package plain

import "testing"

// held runs f and turns a panic into a test failure that names the guard, so a
// removed guard fails by ASSERTION rather than by a panic that ends the binary
// (which mutsweep reads as luck, not proof).
func held(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panicked instead of answering: %v", name, r)
		}
	}()
	f()
}

// plain.go:75: a `<<` with nothing after it has no delimiter.
func TestAHeredocOpenerAtTheEndOfTheCommandDeclines(t *testing.T) {
	for _, s := range []string{"x <<", "x <<   ", "x <<\t"} {
		held(t, "heredocOpener "+s, func() {
			if _, _, ok := heredocOpener(s, len("x <<")); ok {
				t.Errorf("heredocOpener(%q) accepted", s)
			}
		})
	}
}

// plain.go:305 and :330: a refusal on the way is returned, with its reason, and
// the scan does not go on past it.
func TestARefusedConstructStopsTheScan(t *testing.T) {
	for _, tc := range []struct{ cmd, reason string }{
		{"cat <<E `x`\nE", "<< unquoted or odd delimiter"},
		{"cat <<-`x`", "<< unquoted or odd delimiter"},
		{"echo ) `x`", "bare )"},
		{")", "bare )"},
	} {
		held(t, tc.cmd, func() {
			got := check(tc.cmd, true)
			if tc.cmd == ")" {
				// no backtick: never refused, and still no panic
				if got.V != NoTick {
					t.Errorf("%q = %v %q, want NoTick", tc.cmd, got.V, got.Reason)
				}
				return
			}
			if got.V != Uncertain || got.Reason != tc.reason {
				t.Errorf("%q = %v %q, want Uncertain %q", tc.cmd, got.V, got.Reason, tc.reason)
			}
		})
	}
}
