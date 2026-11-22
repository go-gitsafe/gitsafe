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

import "strings"

// StripQuotedHeredocs removes the body of every heredoc whose tag is quoted.
//
// `<<'EOF'` is literal — the shell expands nothing inside it — so text that
// QUOTES a forbidden form is not the forbidden form. Documentation, a commit
// message and a note all need to say what not to do. An unquoted `<<EOF` does
// expand, and its body is left in place to be scanned.
//
// The opener is found by [Classify], so one that sits inside quotes
// (`echo "<<'EOF'"`) opens nothing and strips nothing, and a tag is a shell
// word (`<<'END-MSG'`), not an identifier.
func StripQuotedHeredocs(cmd string) string {
	cls := Classify(cmd)
	var out []string
	for pos := 0; pos <= len(cmd); {
		end := strings.IndexByte(cmd[pos:], '\n')
		next := len(cmd) + 1
		line := cmd[pos:]
		if end >= 0 {
			line, next = cmd[pos:pos+end], pos+end+1
		}
		if pos >= len(cmd) || cls[pos] != ClassHeredocQuoted {
			out = append(out, line)
		}
		pos = next
	}
	return strings.Join(out, "\n")
}

// Statements splits cmd on the separators that end a command, keeping order.
//
// ⛔ `||` and `&&` have to be recognised BEFORE a bare `|`, or every `||` reads
// as a pipeline and `a || b; echo $?` would be refused for nothing.
func Statements(cmd string) []string {
	stmts, _ := StatementsSep(cmd)
	return stmts
}

// StatementsSep is [Statements] and the separator that ENDED each one: seps[i]
// is what joins stmts[i] to stmts[i+1], and is "" for the last statement.
//
// ⛔ WHICH SEPARATOR IT IS DECIDES WHOSE EXIT STATUS SURVIVES, which is why
// this exists beside [Statements] rather than inside a second splitter. With
// `&&` a failing command short-circuits, so the compound's status IS that
// command's; with `;`, `||` or a newline the next statement runs regardless and
// the compound's status is the LAST one's. [bgstatus] asks exactly that
// question, and asking it of its own parse would be two notions of "statement"
// in one repository — the thing this package exists to prevent (see
// [InsideQuotes] for the measured case).
func StatementsSep(cmd string) (stmts []string, seps []string) {
	cur := &strings.Builder{}
	end := func(sep string) {
		stmts = append(stmts, cur.String())
		seps = append(seps, sep)
		cur.Reset()
	}
	q := byte(0)
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		// ⛔ `\"` DOES NOT END THE QUOTING, and nor does `\\`. Measured
		// 2026-11-22 at 21:29, on this session's own command:
		// `git grep -n "a\"\|b\"" -- f` was refused as a pipeline, because the
		// loop here closed the quote at `\"` and then met a bare `|`. The shell
		// does not: the whole pattern is one argument. [InsideQuotes] has
		// honoured the backslash since it was written, which is precisely the
		// two-notions-of-quoted drift its own comment says this package exists
		// to prevent. Inside SINGLE quotes a backslash is an ordinary
		// character, so the skip is for `"` and for unquoted text only.
		if c == '\\' && q != '\'' && i+1 < len(cmd) {
			cur.WriteByte(c)
			i++
			cur.WriteByte(cmd[i])
			continue
		}
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
			end(string(c))
		case '&':
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				end("&&")
				i++
				continue
			}
			cur.WriteByte(c)
		case '|':
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				// `||` ends a statement and is NOT a pipeline.
				end("||")
				i++
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	end("")
	return stmts, seps
}

// InsideQuotes reports whether the byte at off is inside quoting that stops
// the shell interpreting it — so that writing ABOUT a forbidden form is not
// the forbidden form.
//
// ⛔ IT KNOWS BOTH QUOTE CHARACTERS, AND THAT IS WHY IT IS HERE. [Stages] has
// always treated `'` and `"` alike, so a pipeline quoted in a message was
// correctly read as an argument — while the loop rule next door asked a
// private helper that knew only `'`, and refused the same sentence in double
// quotes. Measured 2026-10-31, on this session's own work: an
// `agentsync tell` whose message read "…and go test stays green **while**
// tests drive RootCmd" was refused as a shell loop. Two notions of "quoted"
// in one repository is the thing this package exists to prevent.
//
// ⛔ DOUBLE QUOTES DO NOT STOP A COMMAND SUBSTITUTION, so `$(…)` and
// backticks inside them are reported as NOT quoted: the shell really does run
// what is in there, and a loop hidden in one is a loop.
//
// ⛔ AN UNBALANCED QUOTE IS NOT A QUOTED REGION, it is a broken command, and
// this answers false for every offset in one — a guard that is unsure must
// refuse rather than allow.
func InsideQuotes(s string, off int) bool {
	// An offset outside s is never reached by the loop, so `at` stays false: no
	// bounds check is needed, and none can change the answer.
	q, subst, at := byte(0), 0, false
	for i := 0; i < len(s); i++ {
		if i == off {
			at = q != 0 && subst == 0
		}
		switch c := s[i]; {
		case q == '\'':
			if c == '\'' {
				q = 0
			}
		case c == '\\' && q == '"':
			// `\"` does not end the quoting; nor does `\\`.
			//
			// ⛔ AND THE BYTE IT ESCAPES IS STILL INSIDE THE QUOTES. Skipping it
			// without answering about it reported the `|` in
			// `git grep -n "a\"\|b\"" -- f` as NOT quoted, because the loop
			// never visited that offset at all — the one answer this function
			// must never give by accident, since "unsure" is meant to refuse.
			// Measured 2026-11-22 against [Stages], which had the complementary
			// bug on the same input.
			if i+1 == off {
				at = subst == 0
			}
			i++
		case q == '"' && c == '$' && i+1 < len(s) && s[i+1] == '(':
			subst++
			i++
		case q == '"' && subst > 0 && c == ')':
			subst--
		case q == '"' && c == '`':
			// A backtick substitution has no nesting to count.
			if subst > 0 {
				subst = 0
			} else {
				subst = 1
			}
		case q == '"':
			if c == '"' && subst == 0 {
				q = 0
			}
		case c == '\'' || c == '"':
			q = c
		case c == '\\':
			i++
		}
	}
	if q != 0 || subst != 0 {
		return false // unbalanced: not a quoted region, a broken command
	}
	return at
}

// HasPipe reports whether a statement contains a pipe outside quotes.
func HasPipe(st string) bool {
	return len(Stages(st)) > 1
}

// Stages splits ONE statement into its pipeline stages, on pipes outside quotes.
//
// A statement with no pipe comes back as a single stage, so a caller can always
// speak of stages[0] as the producer. `grep "a|b" f` is one stage: a pipe inside
// quotes is an argument, not a pipeline — and so is `grep "a\"\|b\"" f`, which
// is the shape this loop used to get wrong: see [StatementsSep].
func Stages(st string) []string {
	var out []string
	cur := &strings.Builder{}
	q := byte(0)
	for i := 0; i < len(st); i++ {
		c := st[i]
		if c == '\\' && q != '\'' && i+1 < len(st) {
			cur.WriteByte(c)
			i++
			cur.WriteByte(st[i])
			continue
		}
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
