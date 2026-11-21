package shellcmd

import "testing"

// The transcripts' shape: a quoted line after an arithmetic holding a $( was
// read as code from the $( on (go-gitsafe/gitsafe#129: 62 false matches).
func TestQuotedTextAfterANestedArithmeticIsQuoted(t *testing.T) {
	s := "b=$(( $(wc -c < f) * pct / 100 ))\necho \"=== a ===\"; echo 'x'"
	cls := Classify(s)
	at := func(sub string) int { return indexOf(s, sub) }
	if got := cls[at("=== a")]; got != ClassDouble {
		t.Errorf("class of the quoted === is %q, want ClassDouble %q", got, ClassDouble)
	}
	if got := cls[at("'x'")+1]; got != ClassSingle {
		t.Errorf("class of x in 'x' is %q, want ClassSingle", got)
	}
	if got := cls[at("))")]; got != ClassCode {
		t.Errorf("the )) itself is %q, want code", got)
	}
}

// A # inside is #128's refuse case: dash and zsh run what it was thought to
// hide, so everything after it stays code.
func TestARefusedNestedArithmeticKeepsTheRestAsCode(t *testing.T) {
	s := "echo $(( $(echo 1) #x )); echo 'y'"
	cls := Classify(s)
	if got := cls[len(s)-3]; got != ClassCode {
		t.Errorf("y in 'y' after a refused arithmetic is %q, want code", got)
	}
}

// A backtick inside hides a ) from the count, so the close is not knowable. The
// literaltick table cannot show this guard (the backtick itself is refused
// there), which is why it is asserted on the classes.
func TestABacktickInsideKeepsTheRestAsCode(t *testing.T) {
	s := "echo $(( $(echo `echo )`)); echo 'y'"
	cls := Classify(s)
	if got := cls[len(s)-3]; got != ClassCode {
		t.Errorf("y in 'y' after an arithmetic holding a backtick is %q, want code", got)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	panic("no " + sub)
}
