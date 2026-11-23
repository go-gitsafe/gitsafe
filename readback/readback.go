// Package readback answers one question: does a command line read, in a LATER
// statement, a file that an earlier statement redirected its output into?
//
// # Why it exists
//
// `priorart … > f 2>&1; brief -n 25 -- cat f` reported `exit: 0` while priorart
// had exited 2. The line's status is its LAST statement's — `cat`'s — so the
// measured command's status was thrown away by the very line that shows its
// output, and a false defect report was written from it (the fourth recorded
// occurrence; the third was the background twin, see [bgstatus]).
//
// # What it refuses, stated as the ACT
//
// Redirecting a statement's output to a file and, on the SAME line, naming that
// file as read in a later statement joined by `;`, `||` or a newline. Read the
// file on the NEXT turn, not on this line, or run the command under
// `brief -n N -- <cmd>`, which exits with ITS status.
//
// # What it does NOT refuse
//
//	cmd > f 2>&1                  # nothing is read back
//	cmd > f 2>&1 && brief -n 20 -- cat f   # `&&`: a failure short-circuits
//	a > f; b >> f                 # the later statement WRITES the file too
//	cmd > /dev/null; cat /dev/null
//	echo 'cmd > f; cat f'         # single quotes: writing about the form
package readback

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Writer is the statement that redirects into Target.
	Writer string
	// Reader is the later statement that names Target.
	Reader string
	// Target is the file, as written.
	Target string
	// Sep is the separator that makes the line exit with the reader's status.
	Sep string
}

// Advice is what to do instead: a guard that says no without saying what to do
// gets worked around.
const Advice = "Read the file on the NEXT turn, not on this line. To see the output at once, " +
	"use `brief -n N -- <cmd>`: it runs the command, shows that much of its output, and " +
	"exits with ITS status. `&&` between the two keeps the first failure."

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Writer != "" }

// Why names the act, for a caller that prints it.
func (f Finding) Why() string {
	return "a command line exits with its LAST statement's status, so a line that reads back " +
		"the file it just wrote reports the reader's status, not the command's"
}

func (f Finding) String() string {
	if !f.Found() {
		return ""
	}
	sep := f.Sep
	if sep == "\n" {
		sep = "a newline"
	} else {
		sep = "`" + sep + "`"
	}
	return "`" + f.Reader + "` reads " + f.Target + " after `" + f.Writer + "` wrote it, and " + sep +
		" joins them, so the line exits with the status of the read"
}

// Check says whether cmd reads back, on the same line, a file it redirects into.
func Check(cmd string) Finding {
	cmd = shellcmd.StripQuotedHeredocs(cmd)
	stmts, seps := shellcmd.StatementsSep(cmd)
	words := make([][]string, len(stmts))
	writes := make([][]string, len(stmts))
	for i, st := range stmts {
		words[i], writes[i] = parse(st)
	}
	for i := range stmts {
		for _, target := range writes[i] {
			// sep is the first non-`&&` separator after the writer. Until one
			// appears a failure short-circuits and the status is still the
			// writer's; after it the line exits with a later statement's.
			sep := ""
			for j := i + 1; j < len(stmts); j++ {
				if sep == "" && seps[j-1] != "&&" {
					sep = seps[j-1]
				}
				if sep == "" || contains(writes[j], target) || !contains(words[j], target) {
					continue
				}
				return Finding{
					Writer: strings.TrimSpace(stmts[i]),
					Reader: strings.TrimSpace(stmts[j]),
					Target: target,
					Sep:    sep,
				}
			}
		}
	}
	return Finding{}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// quiet are the targets that are not files anyone reads back.
func quiet(t string) bool {
	switch t {
	case "", "/dev/null", "/dev/stdout", "/dev/stderr", "&1", "&2":
		return true
	}
	return false
}

// parse returns the words of one statement (quotes removed, redirection
// operators dropped, `<` targets kept) and the files it writes to.
func parse(st string) (words, writes []string) {
	var cur strings.Builder
	has := false // cur holds a word, possibly an empty quoted one
	flush := func() {
		if has {
			words = append(words, cur.String())
		}
		cur.Reset()
		has = false
	}
	// word reads one shell word starting at i, honouring quotes.
	word := func(i int) (string, int) {
		var b strings.Builder
		for i < len(st) && (st[i] == ' ' || st[i] == '\t') {
			i++
		}
		for i < len(st) {
			c := st[i]
			if c == ' ' || c == '\t' || c == '>' || c == '<' {
				break
			}
			if c == '\'' || c == '"' {
				q := c
				i++
				for i < len(st) && st[i] != q {
					if q == '"' && st[i] == '\\' && i+1 < len(st) {
						i++
					}
					b.WriteByte(st[i])
					i++
				}
				i++
				continue
			}
			b.WriteByte(c)
			i++
		}
		return b.String(), i
	}
	for i := 0; i < len(st); {
		c := st[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
			i++
		case c == '\'' || c == '"':
			q := c
			has = true
			i++
			for i < len(st) && st[i] != q {
				if q == '"' && st[i] == '\\' && i+1 < len(st) {
					i++
				}
				cur.WriteByte(st[i])
				i++
			}
			i++
		case c == '>':
			// `2>`, `&>`: the digits or ampersand just read belong to the operator.
			if s := cur.String(); !(isFD(s) || s == "&") {
				flush()
			} else {
				cur.Reset()
				has = false
			}
			i++
			// `>>` needs no case of its own: the second `>` is read as an
			// operator with an empty target, then the real one follows.
			amp := i < len(st) && st[i] == '&'
			if amp {
				i++
			}
			var t string
			t, i = word(i)
			// `2>&1`, `>&2`, `>&-`: a descriptor, not a file. `>&f` is a file.
			if amp && (isFD(t) || t == "-") {
				break
			}
			if !quiet(t) {
				writes = append(writes, t)
			}
		case c == '<':
			flush()
			i++
			// `<<` and `<<<` likewise: the extra `<` reads as an empty target.
			var t string
			t, i = word(i)
			if t != "" {
				words = append(words, t)
			}
		default:
			cur.WriteByte(c)
			has = true
			i++
		}
	}
	flush()
	return words, writes
}

func isFD(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
