// Package literaltick answers one question: will the shell RUN something this
// command meant to quote?
//
// # Why it exists
//
// A backtick is command substitution to the shell and a code fence to every
// reader. The two meanings collide in exactly one place — prose typed into a
// command — and the collision is silent: the text is sent, the tool answers
// success, and the words are gone.
//
// Measured on this fleet, the same session, three times:
//
//	agentsync tell peer "… `brandcheck -org` and `vulnscan -selftest` …"
//
// The shell ran `brandcheck -org` and `vulnscan -selftest`, spliced their
// output into the message, and `agentsync` reported `told … 0` — because
// storing a string always succeeds. Two handovers reached their peers mangled,
// and two commands that were never meant to run, ran. The third occurrence was
// inside a message explaining the first two.
//
// ⛔ A NOTE DID NOT STOP IT. The lesson was written down after the first
// occurrence and recurred twice, which is this fleet's own test for when a
// lesson has to become a refusal: before the spend, not after it.
//
// # What it refuses, stated as the ACT
//
// Writing a backtick the shell will evaluate.
//
// That is fully decidable, which is why this rule can be narrow: a backtick
// inside SINGLE quotes is literal and allowed; a backtick anywhere else —
// bare, or inside DOUBLE quotes — is a substitution and refused. Double quotes
// do not stop one, which is the half that surprises people and the half that
// bit here: [shellcmd.Classify] knows it.
//
// # What it does NOT refuse, and that half is the point
//
//	echo 'use `x` in prose'            # single quotes: literal, allowed
//	git commit -F msg.txt              # prose in a file, the advice
//	v=$(git rev-parse HEAD)            # substitution in the modern form
//	echo "cost \`x\` dollars"          # escaped: the shell does not run it
//	grep -n 'func `x`' file.go         # a pattern that happens to hold one
//
// # The escape
//
//	GITSAFE_ALLOW_BACKTICK=1
//
// in front of ONE command, which is an act rather than a setting. There is a
// legitimate use — a deliberate substitution typed in the old form — and
// `$(…)` is the one-character answer to almost all of it.
//
// # What it deliberately does not try to know
//
// INTENT. "Did you mean a code fence or a substitution?" is not answerable from
// the text, and this does not guess: it refuses every backtick the shell would
// evaluate, including a deliberate one. That is the trade, and it is cheap in
// the direction that matters — a deliberate substitution rewrites to `$(…)` and
// is better for it, while a code fence that slipped through is a message whose
// words are gone and whose tool said success.
package literaltick

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Escape is the environment variable that allows one command through.
const Escape = "GITSAFE_ALLOW_BACKTICK"

// Advice is what to do instead. A guard that says no without saying what to do
// gets worked around.
const Advice = "For literal text — prose, a message, a commit body — use SINGLE " +
	"quotes, which stop the shell dead, or write the text to a file and pass the " +
	"file (`git commit -F msg.txt`, `gh pr create --body-file b.md`, a positional " +
	"argument read from a file). For a substitution you actually want, write " +
	"`$(…)`: it cannot be mistaken for a code fence by a reader, which is the " +
	"whole reason the old form is a trap. `" + Escape + "=1` in front of one " +
	"command is the escape."

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Off is the byte offset of the backtick that would be evaluated.
	Off int
	// Context is a short window around it, so a refusal can point at something.
	Context string
	// InDoubleQuotes records the case that surprises people: the backtick sits
	// inside "…" and is still a substitution.
	InDoubleQuotes bool
}

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Context != "" }

// Why names the act, for a caller that prints it.
func (f Finding) Why() string {
	if f.InDoubleQuotes {
		return "a backtick inside double quotes is still a command substitution: " +
			"the shell will RUN what is between the backticks and put its output in " +
			"the text, and the tool that receives the text will report success"
	}
	return "a backtick is a command substitution: the shell will RUN what is " +
		"between the backticks and put its output where you wrote them"
}

func (f Finding) String() string {
	if !f.Found() {
		return ""
	}
	return "it matched " + f.Context
}

// Check says whether cmd contains a backtick the shell would evaluate.
//
// The reading is [shellcmd.Classify]'s: a backtick is literal inside '…' and
// $'…', in a # comment, in a quoted heredoc and after a backslash; everywhere
// else — bare, inside "…", inside $(…) — it runs.
func Check(cmd string) Finding {
	cls := shellcmd.Classify(cmd)
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '`' {
			continue
		}
		switch cls[i] {
		case shellcmd.ClassSingle, shellcmd.ClassComment,
			shellcmd.ClassHeredocQuoted, shellcmd.ClassEscaped:
			continue
		}
		if escapedByPrefix(cmd, cls, i) {
			continue
		}
		return Finding{
			Off:            i,
			Context:        window(cmd, i),
			InDoubleQuotes: cls[i] == shellcmd.ClassDouble,
		}
	}
	return Finding{}
}

// escapedByPrefix reports whether the STATEMENT holding the backtick at off
// carries the escape as an assignment in front of its command.
//
// ⛔ IT IS READ FROM THE TEXT, NOT FROM THE ENVIRONMENT, and the first version
// of this package got that wrong. The hook runs BEFORE any shell, so
// `GITSAFE_ALLOW_BACKTICK=1 some-command` has not been assigned to anything
// when the guard reads it: an `os.Getenv` test can never fire, and the escape
// this package documents would not exist. [volatile] and [bypass] read theirs
// the same way, and [ghbyhand] had the identical defect fixed in v0.37.1.
//
// The corpus row for the escape passed anyway, which is the part worth
// remembering: it was measured against an installed binary that did not carry
// this rule at all, so "allow" was true for the wrong reason.
//
// ⛔ ONE COMMAND, as documented: the prefix covers its own statement, not the
// ones after a ; & | or newline. The first version read the whole line, so
// `GITSAFE_ALLOW_BACKTICK=1 true; echo `date“ was allowed.
func escapedByPrefix(cmd string, cls []byte, off int) bool {
	end := off
	if cls[off] == shellcmd.ClassHeredocExpanded {
		// A heredoc body belongs to the statement that opened it: back up over
		// the body to the newline before it, then keep reading the opener line.
		for end > 0 && cls[end] == shellcmd.ClassHeredocExpanded {
			end--
		}
	}
	start := 0
	for j := end - 1; j >= 0; j-- {
		if cls[j] != shellcmd.ClassCode {
			continue
		}
		c := cmd[j]
		if c == ';' || c == '\n' || c == '|' ||
			(c == '&' && !(j > 0 && (cmd[j-1] == '>' || cmd[j-1] == '<'))) {
			start = j + 1
			break
		}
	}
	for _, f := range strings.Fields(cmd[start:end]) {
		name, value, ok := strings.Cut(f, "=")
		if !ok {
			return false // the first word is the command: no escape in front
		}
		if name == Escape && value != "" && value != "0" {
			return true
		}
	}
	return false
}

// window is a short excerpt around the offset, for the refusal to point at.
func window(s string, off int) string {
	const span = 28
	lo, hi := off-span, off+span
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	out := strings.ReplaceAll(s[lo:hi], "\n", " ")
	if lo > 0 {
		out = "…" + out
	}
	if hi < len(s) {
		out += "…"
	}
	return out
}
