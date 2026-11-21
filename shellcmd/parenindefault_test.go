package shellcmd

import "testing"

// #147: a ( in the default of a double-quoted ${ } is plain text only when
// nothing but double quotes encloses it. Inside a $( ) the classifier bails,
// leaving the rest of the line code, so a later backtick stays visible.
func TestParenInQuotedDefault(t *testing.T) {
	const tick = "`"
	for _, tc := range []struct {
		name, s string
		quoted  bool // the final backtick is classed as quoted text
	}{
		{"plain", "echo \"${x:-(none)}\" '" + tick + "'", true},
		{"inside a quoted $( )", "echo \"$(echo \"${x:-(none)}\")\" '" + tick + "'", false},
		{"inside an unquoted $( )", "echo $(echo \"${x:-(none)}\") '" + tick + "'", false},
		{"inside $(( ))", "echo $(( \"${x:-(none)}\" )) '" + tick + "'", false},
	} {
		cls := Classify(tc.s)
		got := cls[len(tc.s)-2] != ClassCode
		if got != tc.quoted {
			t.Errorf("%s: backtick quoted = %v, want %v for %q", tc.name, got, tc.quoted, tc.s)
		}
	}
}
