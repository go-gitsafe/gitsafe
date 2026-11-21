package literaltick

import "testing"

// Since #156 no shell beyond the one in use (zsh) is modelled. The five inputs
// the pruned bash-3.2.57 balance scan was written for (#133, #136, #146, #153,
// #154) run the backtick in bash 3.2.57 and in neither zsh nor dash. This test
// records the verdict the code gives each TODAY: #133, #146 and #153 are
// ALLOWED; #136 and #154 are refused (by the case-arm and heredoc grammars, not
// by a bash 3.2 model). The paragraph in
// shellcmd/classify.go that says what is refused cannot drift from the code
// again. A row that flips is a decision: change the row and that paragraph.
func TestBash32OnlyShapesAreNotRefusedSince156(t *testing.T) {
	wrap := func(body string) string {
		return "echo --body \"$(cat <<'EOF'\n" + body + "\nEOF\n)\""
	}
	for _, tc := range []struct {
		issue   string
		cmd     string
		refused bool
	}{
		{"#133", wrap("#)\"|" + T), false},
		{"#136", "\"$(<<'E'\n\"E\nE\n\"case x in x) \"case x in x) #" + T + "\"", true},
		{"#146", "echo --body \"$(cat <<'EOF'\n#'\\''\n')\"|" + T + "\nEOF\n)\"", false},
		{"#153", wrap("#\"'\"'\n')\"|" + T), false},
		{"#154", "\"$(<<'E'\n\"E\nE\n)\"case x in x) \";#" + T + "\"", true},
	} {
		// Check, not the package's check helper: that one feeds the oracle,
		// which runs bash, and these inputs are the ones bash alone runs.
		if got := Check(tc.cmd).Found(); got != tc.refused {
			t.Errorf("%s: refused=%v, recorded %v, for %q", tc.issue, got, tc.refused, tc.cmd)
		}
	}
}
