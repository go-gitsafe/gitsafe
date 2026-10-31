// Package shellwork answers one question: is this command DOING work the shell
// should not be doing?
//
// # Why it exists
//
// The machine's own instructions open with it, as their second section:
//
//	⛔ No awk, sed, shell, python or perl — write the tool in Go
//	Bash only invokes tools … no pipes, loops, awk/sed/python/perl
//	Edit files with the Edit and Write tools … Commit messages and PR bodies
//	go in a file written with Write (git commit -F, gh pr create --body-file)
//
// It was read three times in one session and broken all day, by an operator who
// had agreed to it in writing twice. The instruction file says so itself: "The
// user has had to ask for" this repeatedly.
//
// ⛔ AND THE SAME SESSION IS THE CONTROL. The one rule of this family that was
// already a GUARD — a status read after a pipeline, in [pipestatus] — refused
// that operator at 09:35 and could not be worked around. The rule that lived
// only in the instruction file was broken for twelve hours. Same day, same
// person, same file: one enforced, one not.
//
// What it cost, in that one session: a measurement that walked 669 files where
// a Go program walks 1003; a reported "0 of 120 pins are ahead of their tag"
// where there are 8; a whole pass reporting ZERO findings from a column offset,
// which is what a clean fleet looks like; a proxy error sorted to the end of a
// version list and reported as a release; and an exit status read from `tail`
// instead of the command. Five wrong answers, one cause.
//
// # What it refuses, stated as the ACT
//
// Processing data in the shell, rather than invoking a tool that does it.
//
//	awk sed perl python ...     an interpreter doing the work
//	for … do / while … do       a loop
//	a | b                       a pipeline
//	git commit -F -             a body on stdin rather than in a file
//	gh pr create --body-file -  the same
//
// # What it does NOT refuse, and that half is the point
//
//	go test ./...                        invoking a tool
//	go run ./internal/thing              the sanctioned way to process
//	go test ./... > /tmp/out.txt 2>&1    a REDIRECT is not a pipe, and
//	                                     redirect-then-read is what the
//	                                     instructions ask for
//	git commit -F /tmp/msg.txt           a body in a file
//	gh pr create --body-file /tmp/b.md   likewise
//	echo 'do not write a | b'            writing about a form is not that form
//	cat <<'EOF' … EOF                    a quoted heredoc is literal
//	git diff --name-only                 a tool with a flag that reads
//
// # The escape
//
//	GITSAFE_ALLOW_SHELLWORK=1
//
// in front of ONE command, which is an act rather than a setting. It exists
// because this guard is the broadest in the repository and a guard with no exit
// gets switched off wholesale instead of once.
package shellwork

import (
	"regexp"
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing found.
type Finding struct {
	// Kind is the act, in the words of the rule it breaks.
	Kind string
	// Match is the text that showed it, so a refusal points rather than asserts.
	Match string
	// Instead is what to do for this particular kind.
	Instead string
}

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Kind != "" }

// Why names the act.
func (f Finding) Why() string {
	return "it does the work in the shell instead of invoking a tool: " + f.Kind
}

func (f Finding) String() string {
	if !f.Found() {
		return ""
	}
	return "it matched `" + f.Match + "`"
}

// Advice is the general remedy; Finding.Instead is the specific one.
const Advice = "Bash invokes tools; anything that PROCESSES goes in a small Go program " +
	"(`go run ./internal/<name>`), and files are edited with Write or Edit. " +
	"`GITSAFE_ALLOW_SHELLWORK=1` in front of one command is the escape."

// interpreters are the programs whose whole purpose here would be to do the
// work the rule reserves for Go.
//
// ⛔ A NAME LIST, AND IT INHERITS THE BLIND SPOT OF ITS AUTHOR — a sixth
// interpreter nobody thought of goes through. It is a list because the question
// "is this program a general-purpose text processor" has no structural answer,
// and these five are the ones the instruction file names.
var interpreters = map[string]bool{
	"awk": true, "gawk": true, "nawk": true,
	"sed": true, "gsed": true,
	"perl": true, "perl5": true,
	"python": true, "python2": true, "python3": true,
	"ruby": true,
}

// loop matches a shell loop, which is a program and belongs in Go.
//
// ⛔ THE BACKTICK IS IN THE PREFIX CLASS BECAUSE IT OPENS A SUBSTITUTION. A
// loop written as “ `while read -r l; do …; done` “ matched nothing at all
// — not because it was thought quoted, but because the character in front of
// `while` was not one this pattern would start a loop after. `$(…)` was caught
// only by the accident that `(` was already in the class. Found 2026-10-31 by
// a test written for the quoting fix next door, which is the second defect
// that one sentence turned up.
var loop = regexp.MustCompile(`(^|[;&|(` + "`" + `]|\s)(for|while|until)\s+[^;]*;?\s*do(\s|;|$)`)

// stdinBody matches a commit message or a pull-request body taken from stdin
// rather than from a file written with Write.
var stdinBody = regexp.MustCompile(`(--file|-F|--body-file)[\s=]+-(\s|$)`)

// Check says whether cmd does its work in the shell.
func Check(cmd string) Finding {
	// ⛔ Quoting a forbidden form is not the forbidden form: this package's own
	// documentation, a README and a commit message all have to be able to say
	// what not to write.
	cmd = shellcmd.StripQuotedHeredocs(cmd)

	for _, st := range shellcmd.Statements(cmd) {
		stages := shellcmd.Stages(st)
		if len(stages) > 1 {
			return Finding{
				Kind:  "a pipeline",
				Match: strings.TrimSpace(st),
				Instead: "redirect and read the file instead -- `cmd > /tmp/out.txt 2>&1` " +
					"then read it -- which also keeps the command's own exit status",
			}
		}
		for _, stage := range stages {
			if f := inStage(stage); f.Found() {
				return f
			}
		}
	}
	// ⛔ EVERY MATCH IS JUDGED, AND BY THE OFFSET OF THE KEYWORD. Asking about
	// the whole match meant asking about its first character, which is the
	// separator in front of the loop -- for `` `while … done` `` that is the
	// backtick, one byte BEFORE the substitution opens, and so still inside the
	// quoting. The keyword itself is the thing the shell would run. Taking all
	// the matches rather than the first also means a loop written about in a
	// message cannot hide a real one later in the same command line.
	for _, loc := range loop.FindAllStringSubmatchIndex(cmd, -1) {
		if shellcmd.InsideQuotes(cmd, loc[4]) {
			continue // writing ABOUT a loop is not a loop
		}
		return Finding{
			Kind:    "a shell loop",
			Match:   strings.TrimSpace(cmd[loc[0]:loc[1]]),
			Instead: "a loop is a program: put it in a Go program and `go run` it",
		}
	}
	if m := stdinBody.FindString(cmd); m != "" {
		return Finding{
			Kind:    "a commit message or body read from stdin",
			Match:   strings.TrimSpace(m),
			Instead: "write the text to a file with the Write tool and pass its PATH",
		}
	}
	return Finding{}
}

// inStage looks for an interpreter doing the work.
func inStage(stage string) Finding {
	words := strings.Fields(stage)
	for i, w := range words {
		// Skip what stands in front of the real command.
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			continue
		}
		switch base(w) {
		case "command", "env", "sudo", "nice", "time", "nohup", "stdbuf", "timeout", "xargs":
			continue
		}
		if interpreters[base(w)] {
			return Finding{
				Kind:    base(w) + " doing the processing",
				Match:   strings.Join(words[i:min(i+3, len(words))], " "),
				Instead: "write it in Go and `go run` it; edit files with the Write or Edit tool",
			}
		}
		// Only the command word matters: `grep sed x` searches for the word.
		break
	}
	return Finding{}
}

// ⛔ THE `quotedOnly` HELPER THAT USED TO BE HERE IS GONE, and both of its
// faults are worth remembering. It knew only `'`, so this rule refused in
// double quotes what the pipeline rule next door allowed there — measured on
// this session's own work, where an `agentsync tell` message containing the
// English word "while" was refused as a shell loop. And it searched a TRIMMED
// REMAINDER while counting quotes from the start of that remainder, so after
// the first occurrence the parity was computed from the wrong place.
// [shellcmd.InsideQuotes] is now the single answer to "would the shell
// interpret something written here", for every rule that asks.

// base is the last path element, so /usr/bin/awk and awk read alike.
func base(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}
