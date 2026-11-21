package literaltick

import "testing"

// Since #156 no shell beyond the one in use (zsh) is modelled. The inputs the
// pruned bash-3.2.57 balance scan was written for (#133, #136, #146, #153,
// #154, and the corpus seeds of the deleted FuzzBody) run the backtick in bash
// 3.2.57 and in neither zsh nor dash. This test records the verdict the code
// gives each TODAY: every one is ALLOWED. The paragraph in shellcmd/classify.go
// and the README say the same; a row that flips is a decision, so change the
// row and both.
func TestBash32OnlyShapesAreAllowedSince156(t *testing.T) {
	// The FuzzBody grammar as it was when the target was deleted, so its
	// seeds decode to the bodies they were.
	bodyTokens := []string{
		"'", "\"", "`", "$'", "\\", "#", "(", ")", ";", "|", "&", " ", "\n", "x",
		"<<'E'\n", "E\n", "don", "t ", "it", "s", "a", "a ", T, T,
	}
	body := func(data string) string {
		s := ""
		for _, c := range []byte(data) {
			s += bodyTokens[int(c)%len(bodyTokens)]
		}
		return "echo --body \"$(cat <<'EOF'\n" + s + "\nEOF\n)\"\n"
	}
	wrap := func(b string) string {
		return "echo --body \"$(cat <<'EOF'\n" + b + "\nEOF\n)\""
	}
	for _, tc := range []struct{ name, cmd string }{
		{"#133", wrap("#)\"|" + T)},
		{"#136 (FuzzOracle seed)", oracleProgram([]byte("%4,77,C,C\x8f\x1e,"))},
		{"#146", "echo --body \"$(cat <<'EOF'\n#'\\''\n')\"|" + T + "\nEOF\n)\""},
		{"#153", wrap("#\"'\"'\n')\"|" + T)},
		{"#154 (FuzzOracle seed)", oracleProgram([]byte("%4,77&,C,0\x8f\x1e,"))},
		{"FuzzBody issue133-seed2", body("\r\f\x05\a\x01\t\x16")},
		{"FuzzBody issue133-seed3", body("\x11\x05\a\x01\t\x16")},
		{"FuzzBody issue133-seed4", body("\x05\b\a\x01\t\x16")},
		{"FuzzBody issue146-ansic", body("Mc40$0719.")},
	} {
		// Check, not the package's check helper: that one feeds the oracle,
		// which runs bash, and these inputs are the ones bash alone runs.
		if Check(tc.cmd).Found() {
			t.Errorf("%s: refused, recorded allowed, for %q", tc.name, tc.cmd)
		}
	}
}
