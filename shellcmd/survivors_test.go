package shellcmd

import "testing"

// `$(( 1 ) )` is a command substitution holding a subshell, not arithmetic: the
// rest of the command is code, so the quote after it quotes nothing.
func TestArithmeticClosedBySingleParenMakesTheRestCode(t *testing.T) {
	const s = "echo $((1) ) 'x'"
	cls := Classify(s)
	if got := cls[len(s)-2]; got != ClassCode {
		t.Errorf("the x is class %d, want code", got)
	}
}

// A word is judged only when it starts at a boundary: `'a'b` has no word before
// the b, because what precedes it is quoted.
func TestWordBeforeRefusesAWordGluedToQuotedText(t *testing.T) {
	const s = `'a'b`
	if w, k := wordBefore(s, Classify(s), len(s)); w != "" || k != len(s) {
		t.Errorf("wordBefore = %q, %d", w, k)
	}
	const u = "a b"
	if w, k := wordBefore(u, Classify(u), len(u)); w != "b" || k != 2 {
		t.Errorf("wordBefore = %q, %d", w, k)
	}
}

// assignPrefix looks only at a word that is separate from the one before it.
func TestAssignPrefixNeedsABlankBeforeTheWord(t *testing.T) {
	const s = "exportx=(1)"
	if assignPrefix(s, Classify(s), 6) {
		t.Error("assignPrefix true with no blank before the word")
	}
	const ok = "export x=(1)"
	if !assignPrefix(ok, Classify(ok), 7) {
		t.Error("assignPrefix false after export")
	}
}

// A ) with no pattern word before it, or one after a | that has none, ends no
// case arm, even right after `in`.
func TestCaseArmCloseNeedsAPatternWord(t *testing.T) {
	for _, s := range []string{"case x in )", "case x in |b)", "case x in a;; |b)", "case x in a;;|b)"} {
		if caseArmClose(s, Classify(s), len(s)-1) {
			t.Errorf("caseArmClose(%q) = true", s)
		}
	}
	const arm = "case x in b)"
	if !caseArmClose(arm, Classify(arm), len(arm)-1) {
		t.Errorf("caseArmClose(%q) = false", arm)
	}
}

// A heredoc tag is a non-empty word, and an unquoted one cannot start with a
// digit (`1<<2` is a shift); a quoted digit tag is a heredoc.
func TestParseHeredocTags(t *testing.T) {
	for _, s := range []string{"", " ;", "2", "2x"} {
		if _, _, ok := parseHeredoc(s, 0); ok {
			t.Errorf("parseHeredoc(%q) accepted", s)
		}
	}
	for _, s := range []string{"'2'", "E", "-E"} {
		if _, _, ok := parseHeredoc(s, 0); !ok {
			t.Errorf("parseHeredoc(%q) refused", s)
		}
	}
}
