package literaltick

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ⛔ #124, SETTLED BY ASKING THE SHELLS (bash 3.2.57, dash, zsh -f -c), one row
// per variant. What each did with the backtick T, measured on 2026-11-15:
//
//	$(('))'T))          bash RAN (skips '…' while it looks for )), reports
//	                    "operand expected" and goes on); dash: Unterminated
//	                    quoted string; zsh: unmatched '. Nothing ran in dash, zsh.
//	$(('T'))            bash, dash and zsh all RAN: ' is no quote in arithmetic.
//	$(("))"T))          bash RAN (skips "…" too); dash and zsh: unmatched ".
//	$((1 + 2)) 'T'      none ran: no quote inside, the ' after the close quotes.
//
// So bash skips quoted spans in its scan for the )) and dash and zsh do not, and
// the same bytes put the close in different places. No single reading is right
// for all three; the rule refuses a quote inside $(( )) and keeps the rest of
// the line as code. The shells are asked for the refusals (the oracle judges
// every case below under each shell installed); the DECLARATION is the rule's.
func TestArithmeticQuotesAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		refuse    bool
	}{
		{"#124 input 1: ' hides )) from bash", "$(('))'" + T + "))<<'E'\n", true},
		{"#124 input 2: the sibling", "$(('))'))|" + T + "<<'E'", true},
		{"the CI seed bccbf46642f27201", "$(('))'" + T + "))E\n'", true},
		{"' is no quote in arithmetic: all shells run it", "$(('" + T + "'))", true},
		{"a double quote hides )) from bash too", "$((\"))\"" + T + "))", true},
		{"$' inside arithmetic", "$(($'))'" + T + "))", true},
		{"inside double quotes", "\"$(('))'" + T + "))\"", true},
		{"a ) inside ${ } inside $( ): bash's scan ends the substitution there (fuzz)", "\"$(${x:-case x in x) '" + T + "'<<'E'\n\"", true},
		{"a # right after (( )) is a comment in bash (fuzz)", "(( ))#'E\n" + T + "E\n'", true},
		{"no quote inside: the one after the close quotes", "echo $((1 + 2)) '`x`'", false},
		{"a quote before the arithmetic", "echo '(' $((1 + 2)) '`x`'", false},
	} {
		f := check(tc.cmd)
		if f.Found() != tc.refuse {
			t.Errorf("%s: Check(%q).Found() = %v, want %v", tc.name, tc.cmd, f.Found(), tc.refuse)
		}
	}
}

// The corpus files of FuzzOracle that carry these inputs decode to the programs
// they are named for: a corpus entry is token indexes, and a wrong index would
// commit a different program than the one the issue reports.
func TestArithmeticCorpusDecodes(t *testing.T) {
	for name, want := range map[string]string{
		"bccbf46642f27201": "$(('))'" + T + "))E\n'",
		"issue124-input1":  "$(('))'" + T + "))<<'E'\n",
		"issue124-input2":  "$(('))'))|" + T + "<<'E'\n",
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "fuzz", "FuzzOracle", name))
		if err != nil {
			t.Fatal(err)
		}
		_, body, _ := strings.Cut(string(b), "\n")
		body = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(body), "[]byte("), ")")
		s, err := strconv.Unquote(body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := oracleProgram([]byte(s)); got != want {
			t.Errorf("%s decodes to %q, want %q", name, got, want)
		}
	}
}
