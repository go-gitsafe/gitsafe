// Package bgstatus answers one question: does a BACKGROUND command line exit
// with the status of the command it is about?
//
// # Why it exists
//
// The guard next door, [github.com/go-gitsafe/gitsafe/shellwork], refuses a
// pipeline and advises this instead:
//
//	redirect and read the file instead -- `cmd > /tmp/out.txt 2>&1`
//	which also keeps the command's own exit status
//
// The advice is right. The shape it is written in is not, and the repository
// taught it:
//
//	mutsweep -dir . -- go test ./... > $L 2>&1; echo "mutsweep rc=$?" >> $L
//
// ⛔ The status of the COMPOUND is the `echo`'s, which is 0 whatever the tool
// did. In the foreground that costs nothing — the file is read. Started in the
// BACKGROUND, the harness reports the compound's status, and on 2026-11-22 it
// said so twice in twenty minutes:
//
//	Full mutation sweep completed (exit code 0)   mutsweep had exited 1, 2 survivors
//	Wait for the budget … completed (exit code 0)  ratewait had exited 1 on TIMEOUT
//
// Both times the real answer was in the file, and both times reading it was a
// decision rather than something that arrived. The notification is what arrives
// without being asked for, and it said exactly what was hoped for.
//
// # What it refuses, stated as the ACT
//
// Recording a status in the OUTPUT and discarding it from the EXIT STATUS, in a
// command started in the background. The `$?` itself is read correctly — this
// is not [github.com/go-gitsafe/gitsafe/pipestatus], whose doc comment names
// `cmd ; echo $?` among the shapes it must NOT refuse, and is right to: with no
// pipeline the status that `$?` holds IS the command's. The defect is one level
// up, in what the LINE exits with.
//
// # The escape, and why there is none
//
// `brief -n 20 -- <cmd>` runs the command, shows that much of its output, and
// exits with ITS status. It was already written, already installed, and found
// only by `fleetverbs --match "exit status" --text`. An escape would buy
// nothing but the defect, which is the direction recorded for
// [github.com/go-gitsafe/gitsafe/testrun] and
// [github.com/go-gitsafe/gitsafe/ghbyhand].
//
// # What it does NOT refuse, and that half is the point
//
//	cmd > f 2>&1; echo "rc=$?" >> f     # in the FOREGROUND: the file is read
//	brief -n 20 -- cmd > f 2>&1         # the status is the command's
//	cmd && echo "rc=$?"                 # `&&` short-circuits: a failure survives
//	cmd > f 2>&1                        # nothing was discarded
//	cmd > f; echo done                  # no status was claimed to be recorded
//	echo 'write about ; echo $? here'   # single quotes: writing about a form
//
// ⛔ `&&` is the correct spelling and must stay allowed, or the rule has no
// correct form and gets routed around: `brief -n 5 -- a && brief -n 20 -- b`
// reports both statuses and stops at the first failure.
package bgstatus

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/pipestatus"
	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Statement is the statement that records the status, so a refusal can point
	// at something rather than assert.
	Statement string
	// Subject is the statement whose status it records — and whose status the
	// line then throws away.
	Subject string
	// Sep is the separator that joins them: `;`, `||` or a newline. Never `&&`,
	// which keeps the subject's failure.
	Sep string
}

// Advice is what to do instead, named in the refusal: a guard that says no
// without saying what to do gets worked around.
const Advice = "Use `brief -n 20 -- <cmd>`: it runs the command, shows that much of its " +
	"output, and exits with ITS status, so a background notification reports the " +
	"command. For two commands, `&&` between two `brief` calls keeps the first " +
	"failure. Or drop the trailing `echo` and let the notification carry the status."

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Statement != "" }

// Why names the act, for a caller that prints it.
func (f Finding) Why() string {
	return "a background command line exits with its LAST statement's status, so this one " +
		"records the status in its output and discards it from the status the harness reports"
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
	return "`" + f.Statement + "` runs whatever `" + f.Subject + "` did, because " + sep +
		" joins them, so the line exits with the status of the report and not of the command"
}

// Check says whether cmd, started in the background, throws away the status it
// takes care to record.
//
// The background flag is a parameter rather than something read here so the
// rule stays a pure question about the text and its caller, including
// `guardscan` over old transcripts that do not record it.
func Check(cmd string, background bool) Finding {
	if !background {
		return Finding{}
	}
	// ⛔ Quoting a forbidden form is not the forbidden form: a commit message, a
	// README and this package's own documentation all have to be able to say
	// what not to write.
	cmd = shellcmd.StripQuotedHeredocs(cmd)
	stmts, seps := shellcmd.StatementsSep(cmd)
	for i, st := range stmts {
		if !pipestatus.ReadsStatus(st) {
			continue
		}
		// The status it records belongs to the statement before it; the
		// separator between them decides whether the line keeps it.
		for j := i - 1; j >= 0; j-- {
			prev := strings.TrimSpace(stmts[j])
			if prev == "" {
				continue
			}
			if seps[j] == "&&" {
				break
			}
			return Finding{Statement: strings.TrimSpace(st), Subject: prev, Sep: seps[j]}
		}
	}
	return Finding{}
}
