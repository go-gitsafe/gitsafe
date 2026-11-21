package shellcmd

import "testing"

// #147: a `case` word counts only where Classify marks it code.
func TestCaseInCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    string
		want bool
	}{
		{"none", "echo x", false},
		{"code", "case x in", true},
		{"quoted only", "echo 'a case x'", false},
		{"double quoted only", "echo \"a case x\"", false},
		{"quoted then code", "echo 'case x'; case y in", true},
		{"code then quoted", "case y in; echo 'case x'", true},
		{"two quoted", "echo 'a case x' \"b case y\"", false},
		{"heredoc body", "cat <<'M'\nin this case x\nM\n", false},
	} {
		cls := Classify(tc.s)
		if got := caseInCode(tc.s, cls, 0, len(tc.s)); got != tc.want {
			t.Errorf("%s: caseInCode(%q) = %v, want %v", tc.name, tc.s, got, tc.want)
		}
	}
}
