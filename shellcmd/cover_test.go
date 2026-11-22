package shellcmd

import "testing"

// `$$` inside double quotes is the shell's own pid and opens nothing: both bytes
// stay double-quoted text and the quote after them still closes.
func TestDollarDollarInDoubleQuotes(t *testing.T) {
	const s = `echo "$$" x`
	cls := Classify(s)
	for _, i := range []int{6, 7, 8} {
		if cls[i] != ClassDouble {
			t.Errorf("byte %d (%q) is class %d, want double-quoted", i, s[i], cls[i])
		}
	}
	if cls[len(s)-1] != ClassCode {
		t.Errorf("the quote did not close: the last byte is class %d", cls[len(s)-1])
	}
}

// assignPrefix asks only about a word that is separate from what is before it,
// and gives up on a word it cannot read.
func TestAssignPrefixGivesUpWithoutAPrecedingWord(t *testing.T) {
	for _, s := range []string{"a=(1)", "x;a=(1)", "'x' a=(1)", `"x" a=(1)`} {
		k := len(s) - len("a=(1)")
		if assignPrefix(s, Classify(s), k) {
			t.Errorf("assignPrefix(%q, …, %d) = true", s, k)
		}
	}
}

// A case pattern list whose `|` follows a quoted word has no word to read, so it
// is not taken for a case arm.
func TestCaseArmCloseGivesUpOnAQuotedAlternative(t *testing.T) {
	const s = "case x in 'a'|b)"
	if caseArmClose(s, Classify(s), len(s)-1) {
		t.Errorf("caseArmClose(%q) = true", s)
	}
}

// Helpers that fall off the end of their input answer "no".
func TestScannersThatRunOutOfInputSayNo(t *testing.T) {
	const quoted = "echo 'case x'"
	if caseInCode(quoted, Classify(quoted), 0, len(quoted)) {
		t.Error("a quoted `case` was read as code")
	}
	if caseInCode(quoted, Classify(quoted), 3, 3) {
		t.Error("an empty range holds a `case`")
	}
	if k := benignSpan("'abc", 0); k != 0 {
		t.Errorf("benignSpan of an unclosed span = %d", k)
	}
	if end, ok := nestedArithEnd("$(echo hi", 0, 2); ok || end != 0 {
		t.Errorf("nestedArithEnd of an unclosed $( = %d, %v", end, ok)
	}
}

// A backslash outside quotes escapes the quote after it: `\"` opens nothing, so
// the real pair that follows is still a quoted region.
func TestAnEscapedQuoteOutsideQuotesOpensNothing(t *testing.T) {
	const s = `echo \"x "y z"`
	if !InsideQuotes(s, 11) { // the y
		t.Errorf("InsideQuotes(%q, 11) = false", s)
	}
	if InsideQuotes(s, 5) { // the backslash itself
		t.Errorf("InsideQuotes(%q, 5) = true", s)
	}
}

// A `||` that reaches Stages is one boundary, not two.
func TestStagesTreatsOrOrAsOneBoundary(t *testing.T) {
	got := Stages("a || b")
	if len(got) != 2 || got[0] != "a " || got[1] != " b" {
		t.Errorf("Stages = %q", got)
	}
}
