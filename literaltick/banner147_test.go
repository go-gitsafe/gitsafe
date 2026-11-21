package literaltick

import "testing"

// ⛔ #147: Classify read the rest of a line as code after three shapes that a
// script writes around an `echo "=== … ==="` banner, so the quoted banner later
// in the command read as a live backtick (guardscan: 40 false eqword matches over
// the 660133 commands of this machine's transcripts). Each variant is ONE row and
// every row goes through check, so TestTheRuleRefusesWhateverARealShellRuns
// puts it to bash, dash and zsh: an allow row that any of them ran would fail
// there. The refuse rows are the neighbours the narrowing must keep.
//
//	A  $(( $(grep -c 'x' "$L") - 1 ))   quoted spans inside the nested $( )
//	B  "${x:-(none)}"                    a ( in the default of a quoted ${ }
//	C  $(cat <<'M' … in this case … M)   `case` as prose, not as the keyword
func TestBannerShapesOfIssue147(t *testing.T) {
	q := "'" + T + "'"
	for _, tc := range []struct {
		name, cmd string
		refuse    bool
	}{
		// A: allowed
		{"A single and double quoted arguments", "echo $(( $(grep -c 'x' \"$L\") - 1 )); echo " + q, false},
		{"A comment tail", "echo $(( $(grep -c 'x' \"$L\") - 1 )) #" + T, false},
		{"A newline tail", "echo $(( $(grep -c 'x' \"$L\") - 1 ))\necho " + q, false},
		{"A awk program with $1 and braces", "echo $(( $(echo \"$b\"|awk '/m r/{print $1}')/2 )) " + q, false},
		{"A assignment, a pipeline inside", "g=$((g+$(grep -o '<p' \"$f\"|wc -l)))\necho " + q, false},
		{"A double-quoted argument only", "echo $(( $(echo \"x\") + 1 )); echo " + q, false},
		{"A two substitutions, both kinds of quote", "echo $(( $(echo 'x') + $(echo \"y\") )); echo " + q, false},
		{"A inside double quotes", "echo \"$(( $(echo 'x') + 1 ))\" " + q, false},
		// A: refused
		{"A a backtick in the span runs", "echo $(( $(echo '" + T + "') + 1 ))", true},
		{"A a backtick in a double-quoted span runs", "echo $(( $(echo \"" + T + "\") + 1 ))", true},
		{"A a ) in the span", "echo $(( $(echo ')') + 1 )); echo " + q, true},
		{"A a (", "echo $(( $(echo '(') + 1 )); echo " + q, true},
		{"A a )) in the span", "echo $(( $(echo '))') + 1 )); echo " + q, true},
		{"A a )) in a double-quoted span", "echo $(( $(echo \"))\") + 1 )); echo " + q, true},
		{"A a backslash", "echo $(( $(echo 'x\\') + 1 )); echo " + q, true},
		{"A a backslash before the closing double quote", "echo $(( $(echo \"x\\\") + 1 )); echo " + q, true},
		{"A the span never closes", "echo $(( $(echo 'x) + 1 )); echo " + q, true},
		{"A the other quote inside", "echo $(( $(echo '\"') + 1 )); echo " + q, true},
		{"A a # inside", "echo $(( $(echo '#') + 1 )) #" + T, true},
		{"A a $( inside", "echo $(( $(echo \"$(x\") + 1 )); echo " + q, true},
		{"A a ${ inside", "echo $(( $(echo \"${x}\") + 1 )); echo " + q, true},
		{"A a newline inside", "echo $(( $(echo 'x\ny') + 1 )); echo " + q, true},
		{"A a quote at the arithmetic's own level", "echo $(( $(echo 'x') + 'y' )); echo " + q, true},
		{"A the close is not adjacent", "echo $(( $(echo 'x') ) + 1 )); echo " + q, true},

		// B: allowed
		{"B one default", "echo \"${x:-(none)}\" " + q, false},
		{"B comment tail", "echo \"${x:-(none)}\" #" + T, false},
		{"B newline tail", "echo \"${x:-(none)}\"\necho " + q, false},
		{"B an assignment", "v=\"${u:-(no banner)}\"; echo " + q, false},
		{"B two defaults", "printf '%s' \"${a:-(x)}\" \"${b:-(y)}\"; echo " + q, false},
		{"B an unbalanced (", "echo \"${x:-(}\" " + q, false},
		{"B a lone )", "echo \"${x:-)}\" " + q, false},
		{"B a quoted ) after it", "echo \"${x:-(')'}\" " + q, false},
		{"B a newline in the default", "echo \"${x:-(\n}\" " + q, false},
		{"B a nested ${ }", "echo \"${x:-(${y})}\" " + q, false},
		{"B a nested $( )", "echo \"${x:-($(y))}\" " + q, false},
		{"B a ( in the middle of a word", "echo \"${x:-a(b}\" " + q, false},
		{"B a ( in a pattern", "echo \"${x/(/}\" " + q, false},
		{"B zsh parameter flags", "echo \"${(f)x}\" " + q, false},
		// B: refused
		{"B a backtick in the default runs", "echo \"${x:-(" + T + ")}\"", true},
		{"B a # in the default", "echo \"${x:-(#}\" #" + T, true},
		{"B a double quote in the default", "echo \"${x:-(\"}\" " + q, true},
		{"B a backslash in the default", "echo \"${x:-(\\}\" " + q, true},
		{"B a heredoc in the default", "echo \"${x:-(<<'E'\n}\"\nE\n" + q, true},
		{"B zsh reads a ( after the word as a glob group (#121)", "echo \"${x:-(}\"(E\n#" + T, true},
		{"B unquoted ${ }", "echo ${x:-(none)} " + q, true},
		{"B inside $( )", "echo $(echo \"${x:-(none)}\") " + q, true},
		{"B inside \"$( )\"", "echo \"$(echo \"${x:-(none)}\")\" " + q, true},

		// C: allowed
		{"C quoted heredoc, ( in the prose", "echo \"$(cat <<'M'\nin this case (x)\nM\n)\" " + q, false},
		{"C quoted heredoc", "echo \"$(cat <<'M'\nin this case x\nM\n)\" " + q, false},
		{"C a ) in the prose", "echo \"$(cat <<'M'\nin this case x) y\nM\n)\" " + q, false},
		{"C a whole case line as prose", "echo \"$(cat <<'M'\ncase x in x) y\nM\n)\" " + q, false},
		{"C unquoted heredoc", "echo \"$(cat <<M\nin this case x\nM\n)\" " + q, false},
		{"C single-quoted argument", "echo \"$(echo 'a case x')\" " + q, false},
		{"C double-quoted argument", "echo \"$(echo \"a case x\")\" " + q, false},
		{"C unquoted $( )", "echo $(cat <<'M'\nin this case x\nM\n) " + q, false},
		// C: refused
		{"C a real case", "echo \"$(case x in x) :;; esac)\" " + q, true},
		{"C a real case after a quoted one", "echo \"$(echo 'case'; case x in x) :;; esac)\" " + q, true},
		{"C a real case after a heredoc", "echo \"$(cat <<'M'\nx\nM\ncase x in x) :;; esac)\" " + q, true},
		{"C a real case after prose", "echo \"$(cat <<'M'\nin this case x\nM\ncase y in y) :;; esac)\" " + q, true},
		{"C a comment holding the word", "echo \"$(echo a # case x\n)\" " + q, true},
	} {
		f := check(tc.cmd)
		if f.Found() != tc.refuse {
			t.Errorf("%s: Check(%q).Found() = %v, want %v", tc.name, tc.cmd, f.Found(), tc.refuse)
		}
	}
}
