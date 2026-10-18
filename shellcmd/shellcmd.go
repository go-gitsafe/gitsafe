// Package shellcmd holds the reading of a shell command line that more than one
// guard needs.
//
// It exists because two rules ask the same preliminary question — "is this text
// a command, or is it a command being WRITTEN ABOUT?" — and the answer has to be
// the same for both. A guard that refused a commit message for quoting the thing
// it warns against would be worked around by the end of the day.
//
// [Statements], [Stages] and [HasPipe] arrived here for the same reason, from
// [pipestatus] where they were private: a second rule — [sigpipe] — needs the
// same split, and two guards that disagreed about where a pipeline begins would
// refuse and allow the same command. The `||` ordering trap below is the kind of
// thing that must be got right once, not twice.
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

// Statements splits cmd on the separators that end a command, keeping order.
//
// ⛔ `||` and `&&` have to be recognised BEFORE a bare `|`, or every `||` reads
// as a pipeline and `a || b; echo $?` would be refused for nothing.
func Statements(cmd string) []string {
	var out []string
	cur := &strings.Builder{}
	q := byte(0)
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if q != 0 {
			cur.WriteByte(c)
			if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			q = c
			cur.WriteByte(c)
		case ';', '\n':
			out = append(out, cur.String())
			cur.Reset()
		case '&':
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				out = append(out, cur.String())
				cur.Reset()
				i++
				continue
			}
			cur.WriteByte(c)
		case '|':
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				// `||` ends a statement and is NOT a pipeline.
				out = append(out, cur.String())
				cur.Reset()
				i++
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	return out
}

// HasPipe reports whether a statement contains a pipe outside quotes.
func HasPipe(st string) bool {
	return len(Stages(st)) > 1
}

// Stages splits ONE statement into its pipeline stages, on pipes outside quotes.
//
// A statement with no pipe comes back as a single stage, so a caller can always
// speak of stages[0] as the producer. `grep "a|b" f` is one stage: a pipe inside
// quotes is an argument, not a pipeline.
func Stages(st string) []string {
	var out []string
	cur := &strings.Builder{}
	q := byte(0)
	for i := 0; i < len(st); i++ {
		c := st[i]
		if q != 0 {
			cur.WriteByte(c)
			if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			q = c
			cur.WriteByte(c)
		case '|':
			// A `||` reaching here would be a statement separator, which
			// Statements has already handled; treat it as one stage boundary
			// rather than two empty ones.
			if i+1 < len(st) && st[i+1] == '|' {
				i++
			}
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	return out
}
