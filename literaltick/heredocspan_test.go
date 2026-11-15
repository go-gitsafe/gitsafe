package literaltick

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ⛔ #136, found by FuzzOracle. bash 3.2.57's scan of a $( ) has no notion of a
// heredoc: a lone " in a QUOTED body pairs with the next " anywhere after it,
// the terminator line included, so the terminator is hidden, the substitution
// closes at a later ), and what follows is code. Measured 2026-11-15 under
// bash 3.2.57 / dash / zsh -f -c (only bash runs the marker):
//
//	$(<<'E' NL "E NL E NL "case x in x) "case x in x) #T"   bash RAN (the fuzz seed)
//	same with q instead of "E                                 nothing ran: no span
//	$(<<'E' NL "E NL E NL ")"x) #T"                           bash RAN
//	$(<<'E' NL "E NL E NL "x) "x) T"                          bash RAN
//
// So the body is unsure when it ends inside a span whose quote occurs again
// after the body. Each row DECLARES the verdict; the shells corroborate in
// TestHeredocSpanCutsAreReal.
func TestAHeredocBodyThatLeavesASpanOpen(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		refuse    bool
	}{
		{"the FuzzOracle seed", "$(<<'E'\n\"E\nE\n\"case x in x) \"case x in x) #" + T + "\"", true},
		{"a ) between the two quotes", "$(<<'E'\n\"E\nE\n\")\"x) #" + T + "\"", true},
		{"two short spans", "$(<<'E'\n\"E\nE\n\"x) \"x) " + T + "\"", true},
		{"a different word in the spans", "$(<<'E'\n\"E\nE\n\"y) \"y) #" + T + "\"", true},
		{"an odd ' in the body, a ' after it", "$(<<'E'\n'E\nE\n')'x) #" + T + "'", true},
		{"no quote in the body", "$(<<'E'\nq\nE\n\"case x in x) \"case x in x) #" + T + "\"", false},
		{"a lone \" pairing with the frame's own closing \": nothing runs", "echo --body \"$(cat <<'E'\n\"x " + T + "\nE\n)\"\n", false},
		{"the same with a lone '", "echo --body \"$(cat <<'E'\n'x " + T + "\nE\n)\"\n'", false},
	} {
		if got := check(tc.cmd).Found(); got != tc.refuse {
			t.Errorf("%s: the rule refuses = %v, declared %v\n  %q", tc.name, got, tc.refuse, tc.cmd)
		}
	}
}

func TestHeredocSpanCutsAreReal(t *testing.T) {
	shells := oracleShells(t)
	for name, cmd := range map[string]string{
		"the seed":        "$(<<'E'\n\"E\nE\n\"case x in x) \"case x in x) #" + T + "\"",
		"a ) in between":  "$(<<'E'\n\"E\nE\n\")\"x) #" + T + "\"",
		"two short spans": "$(<<'E'\n\"E\nE\n\"x) \"x) " + T + "\"",
	} {
		if !check(cmd).Found() {
			t.Errorf("%s: the rule allows %q", name, cmd)
		}
		ran := false
		for _, sh := range shells {
			ran = ran || runs(t, sh, cmd)
		}
		t.Logf("%s: an installed shell ran the backtick = %v", name, ran)
	}
}

func TestHeredocSpanCorpusDecodes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "fuzz", "FuzzOracle", "7745f353e45bb813"))
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ := strings.Cut(string(b), "\n")
	body = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(body), "[]byte("), ")")
	s, err := strconv.Unquote(body)
	if err != nil {
		t.Fatal(err)
	}
	want := "$(<<'E'\n\"E\nE\n\"case x in x) \"case x in x) #" + T + "\""
	if got := oracleProgram([]byte(s)); got != want {
		t.Errorf("seed decodes to %q, want %q", got, want)
	}
}
