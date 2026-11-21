package literaltick

import "testing"

// ⛔ A $( INSIDE $(( … )), SETTLED BY ASKING THE SHELLS (bash 3.2.57, dash,
// zsh -f -c; the probe printed which shell ran T per row, 2026-11-16).
//
// Classify used to read EVERYTHING after such a $( as code, so a quoted span
// later in the command was a live backtick: guardscan counted 62 false eqword
// matches that way, and it is the shape #128 keeps refused.
//
//	$(( $(echo 1) + 2 )) then '…T…', # T, #T or a newline and '…T…':
//	    no shell ran T. The arithmetic ends at the )) in all three.
//	likewise for $(( n OP $(c) )), two substitutions, $(wc -c < f), ($(c)).
//	$(( $(echo 1) #T))       dash and zsh RAN T (a comment cannot hide it): #128.
//	$(( $(echo 1) 'T' ))     bash, dash and zsh all RAN T.
//
// So the frame ends at the close only when what lies between holds none of: a
// quote, a #, a backtick, a backslash, a newline, ${ $[ $(( << , a `case`, or a
// close that is not the two adjacent )) at the frame's own depth. Each variant
// below is one row, and the oracle judges every allow row under every shell.
func TestNestedArithmeticClosesAtTheMatchingParens(t *testing.T) {
	q := "'" + T + "'"
	for _, tc := range []struct {
		name, cmd string
		refuse    bool
	}{
		{"substitution then operator, quoted tail", "echo $(( $(echo 1) + 2 )); echo " + q, false},
		{"substitution alone, comment tail", "echo $(( $(echo 1) )) #" + T, false},
		{"operand after an operator", "echo $(( 1 + $(echo 1) ))\necho " + q, false},
		{"two substitutions", "echo $(( $(echo 1) + $(echo 2) )) " + q, false},
		{"the transcripts' shape, a < redirect inside", "echo $(( $(wc -c < f) * 2 / 100 )); echo " + q, false},
		{"parenthesised substitution", "echo $(( ($(echo 1)) + 2 )); # " + T, false},
		{"no spaces", "echo $(( $(echo 1)+2 )) " + q, false},
		{"inside double quotes", "echo \"$(( $(echo 1) + 2 ))\" " + q, false},
		// The one command guardscan saw leave the refused list (622 -> 621): a
		// quoted heredoc whose body holds a backtick, after a nested arithmetic.
		{"a quoted heredoc body after it is literal", "echo \"A/B: $(( $(grep -c . x 2>/dev/null) / 2 ))/154\"\ncat > t.go <<'EOF'\ne.Run(" + T + ")\nEOF\n", false},
		{"after an assignment", "b=$(( $(wc -c < f) * 2 ))\necho " + q, false},

		{"#128: a # inside runs in dash and zsh", "echo $(( $(echo 1) #" + T + "))", true},
		{"the same, quoted tail", "echo $(( $(echo 1) #" + T + ")); echo " + q, true},
		{"a quote inside runs in all three", "echo $(( $(echo 1) " + q + " ))", true},
		{"a # hides a )) from nothing: no backtick in it", "echo $(( $(echo 1) #x)); echo " + q, true},
		{"a quoted ) closes nothing", "echo $(( $(echo ')')); echo " + q, true},
		{"a ) inside backticks closes nothing", "echo $(( $(echo `echo )`)); echo " + q, true},
		{"a quoted ) inside", "echo $(( $(echo ')') + 2 )); echo " + q, true},
		{"a double quote inside", "echo $(( $(echo 1) + \"x\" )); echo " + q, true},
		{"a backtick substitution inside", "echo $(( $(echo 1) + `echo 2` )); echo " + q, true},
		{"an escaped ) inside closes nothing", "echo $(( $(echo \\))); echo " + q, true},
		{"$[ inside", "echo $(( $(echo 1) + $[1] )); echo " + q, true},
		{"${ inside", "echo $(( $(echo 1) + ${x} )); echo " + q, true},
		{"a nested $((", "echo $(( $(echo 1) + $((1)) )); echo " + q, true},
		{"a heredoc operator inside", "echo $(( $(cat <<E) + 2 )); echo " + q, true},
		{"the close is not adjacent: a space between", "echo $(( $(echo 1) + 2 ) ); echo " + q, true},
		{"the close is not adjacent: a stray ) first", "echo $(( $(echo 1) ) + 2 )); echo " + q, true},
		{"unclosed", "echo $(( $(echo 1) + 2 ); echo " + q, true},
		{"a case whose ) closes nothing", "echo $(( $(case x in x) echo 1;; esac) + 2 )); echo " + q, true},
		{"a case whose ) would count as the close", "echo $(( $(case x in x) echo 1;; esac))); echo " + q, true},
		{"a comment inside the substitution", "echo $(( $(echo 1 # c\n) + 2 )); echo " + q, true},
		{"a newline inside", "echo $(( $(echo 1\n) + 2 )); echo " + q, true},
		{"(( is not $((: dash reads two subshells", "(( $(echo 1) + 2 )); echo " + q, true},
	} {
		f := check(tc.cmd)
		if f.Found() != tc.refuse {
			t.Errorf("%s: Check(%q).Found() = %v, want %v", tc.name, tc.cmd, f.Found(), tc.refuse)
		}
	}
}
