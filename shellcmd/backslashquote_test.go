package shellcmd

import (
	"strings"
	"testing"
)

// refused is the command guard-bash refused at 21:29 on 2026-11-22, in this
// repository, while the session was adding a rule to it. zsh reads the whole
// pattern as ONE argument: inside double quotes `\"` is a quote character and
// `\|` is a backslash and a pipe, neither of them a pipeline.
const refused = `git grep -n "a\"\|b\"" -- f`

// ⛔ ONE NOTION OF "QUOTED", AND IT HAS TO BE THE SAME ONE. [InsideQuotes] has
// honoured the backslash since it was written, and said so in a comment about
// why two notions in one repository is the thing this package prevents — while
// [Stages] and [Statements] next door closed the quoting at `\"` and then met a
// bare `|`. The guard refused a `git grep`.
func TestABackslashedQuoteDoesNotEndTheQuoting(t *testing.T) {
	if st := Stages(refused); len(st) != 1 {
		t.Errorf("Stages found %d stage(s) in a quoted pattern: %q", len(st), st)
	}
	if HasPipe(refused) {
		t.Error("HasPipe: a `\\|` inside double quotes is not a pipeline")
	}
	// The same byte, asked of the shared notion: it is inside the quotes, and
	// the honest answer is not "no" just because the scan stepped over it.
	i := strings.IndexByte(refused, '|')
	if i < 0 {
		t.Fatal("the witness has no pipe in it")
	}
	if !InsideQuotes(refused, i) {
		t.Errorf("InsideQuotes(%d) = false for a byte inside the double quotes", i)
	}
}

// A separator escaped or quoted the same way must not split a statement either.
func TestABackslashedQuoteDoesNotEndAStatement(t *testing.T) {
	for _, cmd := range []string{
		`echo "a\"; b"`,
		`echo "a\"&& b"`,
		`agentsync tell r "write \"cmd > f; echo rc\" here"`,
	} {
		if stmts, _ := StatementsSep(cmd); len(stmts) != 1 {
			t.Errorf("%q: %d statement(s), want 1: %q", cmd, len(stmts), stmts)
		}
	}
}

// ⛔ And the other direction, which is what keeps this from being a hole: a
// backslash inside SINGLE quotes is an ordinary character, and a real separator
// after the closing quote still separates.
func TestTheSkipDoesNotSwallowARealSeparator(t *testing.T) {
	for cmd, want := range map[string]int{
		`echo 'a\'; b`:    2, // single quotes end at the `'`, so the `;` is real
		`echo "a\\"; b`:   2, // `\\` is an escaped backslash: the quote closes
		`a; b`:            2,
		`echo "a;b"`:      1,
		`go build | tail`: 1, // one statement, two stages
	} {
		if stmts, _ := StatementsSep(cmd); len(stmts) != want {
			t.Errorf("%q: %d statement(s), want %d: %q", cmd, len(stmts), want, stmts)
		}
	}
	if st := Stages(`go build | tail`); len(st) != 2 {
		t.Errorf("a real pipeline became %d stage(s): %q", len(st), st)
	}
}
