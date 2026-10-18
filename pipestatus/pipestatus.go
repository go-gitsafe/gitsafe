// Package pipestatus answers one question: does this command read an exit status
// that belongs to the wrong command?
//
// # Why it exists
//
// A pipeline's status is its LAST stage's. So this reports whether `tail`
// succeeded, which it always does:
//
//	go build ./... | tail -5 ; echo "status=$?"
//
// On 2026-10-11 that shape was written three times in one day, in three
// spellings — after `go build | head`, after `go build | tail`, and after a
// fleet scan piped into `tail -40`. Every one of them printed `status=0`. The
// build had failed and the scan had found 350 carriers; both read as success.
//
// There is a memory for it. The memory did not help: the same operator had read
// it, written it down again, and typed the shape three more times the same
// afternoon. That is the pattern this repository exists for — a rule that has to
// be remembered is a rule that will eventually be forgotten.
//
// # What it refuses, stated as the ACT
//
// Reading a status that the pipeline did not produce. Not one spelling of it:
// `echo $?`, `exit $?`, `if [ $? -ne 0 ]`, `code=$?` are the same mistake, and a
// guard that named only the first would send whoever hit it to the second.
//
// # The escape, and why it is the right one
//
//	set -o pipefail
//
// With pipefail the status IS the pipeline's, so the command becomes correct
// rather than merely permitted. `${PIPESTATUS[0]}` is the other correct answer.
// Both are named in the refusal, because a guard that says no without saying
// what to do instead gets worked around.
//
// # What it does NOT refuse, and that half is the point
//
//	cmd ; echo $?                      # no pipeline: the status is the command's
//	set -o pipefail; a | b; echo $?    # the status is the pipeline's
//	a | b                              # nobody read a status
//	echo "write about | and $? here"   # quoting a form is not that form
//	a | b && echo ok                   # a status USED, not read into a report
//
// # What it deliberately cannot cover
//
// The sibling mistake from the same day is not decidable here: `| tail -40` on a
// report whose BODY is the answer threw away 350 lines and kept the summary. A
// `tail` is right for a build log, where the end is the verdict, and wrong for a
// census, where the end is the summary. Nothing in the command line says which
// one it is, so that one stays a note and this package does not pretend to it.
package pipestatus

import (
	"regexp"
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Statement is the statement that read the status, so a refusal can point at
	// something rather than assert.
	Statement string
	// Pipeline is the statement whose status it is NOT.
	Pipeline string
}

// Advice is what to do instead, named in the refusal: a guard that says no
// without saying what to do gets worked around.
const Advice = "Set `set -o pipefail` first, which makes the status the PIPELINE's " +
	"rather than merely permitting the read; or use ${PIPESTATUS[0]} for the first " +
	"stage's; or run the command on its own and read its status, letting a second " +
	"command format the output."

// Why names the act, for a caller that prints it.
func (f Finding) Why() string {
	return "it reads an exit status that belongs to the last stage of a pipeline, not to the command whose result is wanted"
}

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Statement != "" }

func (f Finding) String() string {
	if !f.Found() {
		return ""
	}
	return "`" + f.Statement + "` reads $? after the pipeline `" + f.Pipeline +
		"`, so the status is the LAST stage's and not the pipeline's"
}

// readsStatus matches a read of $? in any of its spellings.
var readsStatus = regexp.MustCompile(`\$\?|\$\{\?\}`)

// pipefail is the escape: with it set, the status IS the pipeline's.
var pipefail = regexp.MustCompile(`set\s+-[a-zA-Z]*o\s+pipefail|set\s+-o\s+pipefail`)

// Check says whether cmd reads a status the pipeline before it did not produce.
func Check(cmd string) Finding {
	// ⛔ Quoting a forbidden form is not the forbidden form: a commit message, a
	// README and this package's own documentation all have to be able to say
	// what not to write.
	cmd = shellcmd.StripQuotedHeredocs(cmd)
	if pipefail.MatchString(cmd) {
		return Finding{}
	}
	stmts := shellcmd.Statements(cmd)
	for i, st := range stmts {
		if !readsStatus.MatchString(st) || quotedOnly(st) {
			continue
		}
		// The status belongs to the statement before this one.
		for j := i - 1; j >= 0; j-- {
			prev := strings.TrimSpace(stmts[j])
			if prev == "" {
				continue
			}
			if shellcmd.HasPipe(prev) {
				return Finding{Statement: strings.TrimSpace(st), Pipeline: prev}
			}
			break
		}
	}
	return Finding{}
}

// quotedOnly reports whether every $? in the statement is inside single quotes,
// where the shell does not expand it: writing ABOUT the form is not the form.
//
// Double quotes DO expand, so `echo "status=$?"` is the mistake itself and is
// not excused here — which is exactly the spelling that was typed three times.
func quotedOnly(st string) bool {
	q := byte(0)
	for i := 0; i < len(st); i++ {
		c := st[i]
		if q != 0 {
			if c == q {
				q = 0
				continue
			}
			if q == '\'' {
				continue // inside single quotes: not an expansion
			}
		}
		switch c {
		case '\'', '"':
			if q == 0 {
				q = c
			}
		case '$':
			if q != '\'' && i+1 < len(st) && (st[i+1] == '?' || st[i+1] == '{') {
				return false
			}
		}
	}
	return true
}
