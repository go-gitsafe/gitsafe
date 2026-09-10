// Package shellcmd holds the reading of a shell command line that more than one
// guard needs.
//
// It exists because two rules ask the same preliminary question — "is this text
// a command, or is it a command being WRITTEN ABOUT?" — and the answer has to be
// the same for both. A guard that refused a commit message for quoting the thing
// it warns against would be worked around by the end of the day.
package shellcmd

import (
	"regexp"
	"strings"
)

// A quoted heredoc tag: <<'EOF', <<"EOF", <<\EOF, with an optional dash.
var heredocOpen = regexp.MustCompile(`<<-?\s*(?:'([A-Za-z_][A-Za-z0-9_]*)'|"([A-Za-z_][A-Za-z0-9_]*)"|\\([A-Za-z_][A-Za-z0-9_]*))`)

// StripQuotedHeredocs removes the body of every heredoc whose tag is quoted.
//
// `<<'EOF'` is literal — the shell expands nothing inside it — so text that
// QUOTES a forbidden form is not the forbidden form. Documentation, a commit
// message and a note all need to say what not to do. An unquoted `<<EOF` does
// expand, and its body is left in place to be scanned.
func StripQuotedHeredocs(cmd string) string {
	lines := strings.Split(cmd, "\n")
	var out []string
	tag := ""
	for _, line := range lines {
		if tag != "" {
			if strings.TrimSpace(line) == tag {
				tag = ""
			}
			continue
		}
		if m := heredocOpen.FindStringSubmatch(line); m != nil {
			for _, g := range m[1:] {
				if g != "" {
					tag = g
					break
				}
			}
			// The opening line itself is kept: a command may both open a heredoc
			// and carry a substitution of its own.
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
