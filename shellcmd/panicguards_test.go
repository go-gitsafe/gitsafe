package shellcmd

import "testing"

// held runs f and turns a panic into a test failure that names the guard, so a
// removed guard fails by ASSERTION: a panic that kills the test binary would
// also end every test after it, and mutsweep reads that as luck, not proof.
func held(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panicked instead of answering: %v", name, r)
		}
	}()
	f()
}

// classify.go:60: the stack is empty at the top level, and that is code.
func TestAnEmptyFrameStackIsCode(t *testing.T) {
	held(t, "Classify on a plain word", func() {
		s := "echo x"
		cls := Classify(s)
		if len(cls) != len(s) {
			t.Fatalf("Classify(%q) has %d classes, want %d", s, len(cls), len(s))
		}
		for i, c := range cls {
			if c != ClassCode {
				t.Errorf("Classify(%q)[%d] = %v, want ClassCode", s, i, c)
			}
		}
	})
}

// classify.go:483: a command may start at offset 0, with nothing before it.
func TestACommandMayStartAtOffsetZero(t *testing.T) {
	for _, s := range []string{"x", "  x", "\tx"} {
		held(t, "commandPos "+s, func() {
			if !commandPos(s, Classify(s), 0) {
				t.Errorf("commandPos(%q, 0) = false", s)
			}
		})
	}
}

// classify.go:640: an unclosed quote in a heredoc tag is no heredoc.
func TestAnUnclosedQuoteInAHeredocTagIsNoHeredoc(t *testing.T) {
	for _, s := range []string{"'abc", `"abc`, "x'"} {
		held(t, "parseHeredoc "+s, func() {
			j := 0
			if s == "x'" {
				j = 1
			}
			if _, _, ok := parseHeredoc(s, j); ok {
				t.Errorf("parseHeredoc(%q, %d) accepted an unclosed quote", s, j)
			}
		})
	}
}

// classify.go:726: no `case` word at all is not a case.
func TestNoCaseWordIsNotACase(t *testing.T) {
	for _, s := range []string{"echo", "", "cases and showcase"} {
		held(t, "caseInCode "+s, func() {
			if caseInCode(s, Classify(s), 0, len(s)) {
				t.Errorf("caseInCode(%q) = true", s)
			}
		})
	}
}
