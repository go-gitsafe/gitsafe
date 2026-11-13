// Command guard-bash refuses a shell command that would put a secret on a
// command line, before it runs.
//
// It reads a Claude Code PreToolUse payload on stdin and answers with a deny
// decision, or says nothing and lets the command through. Wire it up with
//
//	// ~/.claude/settings.json
//	{"hooks": {"PreToolUse": [{"matcher": "Bash",
//	  "hooks": [{"type": "command", "command": "guard-bash", "timeout": 10}]}]}}
//
// # Why a hook and not a note
//
// The rule against a secret on a command line is written down here in three
// places. It was read, understood, and broken anyway — the same operator typed
//
//	curl -H "Authorization: Bearer $(cat ~/.github-token)" …
//
// about thirty times in one session, hours after re-reading the rule and
// writing it down again. It reads as careful because the secret is never typed;
// the substitution runs first, and what reaches execve is the token.
//
// A rule that has to be remembered is a rule that will eventually be forgotten.
// This one is executed by the harness, not by a person.
//
// The decision lives in [secretarg] so it can be tested against a table of
// cases — including the ones it must NOT refuse, which are the half that keeps
// a guard from being worked around.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/go-gitsafe/gitsafe/bypass"
	"github.com/go-gitsafe/gitsafe/chainedgate"
	"github.com/go-gitsafe/gitsafe/discard"
	"github.com/go-gitsafe/gitsafe/ghbyhand"
	"github.com/go-gitsafe/gitsafe/literaltick"
	"github.com/go-gitsafe/gitsafe/pipestatus"
	"github.com/go-gitsafe/gitsafe/secretarg"
	"github.com/go-gitsafe/gitsafe/shellwork"
	"github.com/go-gitsafe/gitsafe/sigpipe"
	"github.com/go-gitsafe/gitsafe/stalebase"
	"github.com/go-gitsafe/gitsafe/testrun"
	"github.com/go-gitsafe/gitsafe/volatile"
)

// payload is the part of the PreToolUse event this reads.
type payload struct {
	// Cwd is the directory the command will run in; the harness sends it with
	// every event.
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command         string `json:"command"`
		RunInBackground bool   `json:"run_in_background"`
	} `json:"tool_input"`
}

// decision is the deny answer the harness understands.
type decision struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func main() { os.Exit(run(os.Stdin, os.Stdout)) }

// run always exits 0. A guard that fails closed on its own bug would block
// every command on the machine; one that fails open lets through exactly the
// commands it could not read, which is the lesser harm and the honest one —
// unreadable input means no opinion, not approval.
func run(stdin io.Reader, stdout io.Writer) int {
	b, err := io.ReadAll(stdin)
	if err != nil {
		return 0
	}
	var p payload
	if err := json.Unmarshal(b, &p); err != nil || p.ToolInput.Command == "" {
		return 0
	}
	// A command that has a CHECKING wrapper, run by hand. These three rules
	// lived in a shell script registered beside this binary, as greps over the
	// whole command text. Measured on 2026-09-29 the script was wrong on 6 of
	// 10 cases — four disclosures let through and two correct commands refused
	// — and every false refusal was a person WRITING about a command rather
	// than running one. See [bypass].
	if f := bypass.Check(p.ToolInput.Command); f.Found() {
		return deny(stdout, fmt.Sprintf(
			"Refused: %s.\n\nIt matched %s\n\n%s",
			f.Why, f.Match, f.Advice))
	}
	if f := secretarg.Check(p.ToolInput.Command); f.Found() {
		return deny(stdout, fmt.Sprintf(
			"Refused: %s.\n\nIt matched %s\n\n%s\n\nIf the value is NOT a secret, name the file something that does not read as one.",
			f.Why, f.Match, secretarg.Advice))
	}
	// Work that exists only in the working tree is gone the moment it is
	// overwritten: there is no reflog for a file that was never committed. The
	// escape is read here rather than inside the rule so that the rule stays a
	// pure question about the text.
	if os.Getenv("GITSAFE_ALLOW_DISCARD") == "" {
		if f := discard.Check(p.ToolInput.Command); f.Found() {
			return deny(stdout, fmt.Sprintf(
				"Refused: %s\n\nIt matched %s\n\n%s",
				f.Why, f.Match, discard.Advice))
		}
	}
	// ⛔ A working tree in a directory this machine empties is the only copy of
	// its commits until they are pushed. The session scratchpad is under
	// /private/tmp; five losses were written down before this was a guard, the
	// last a whole branch on 2026-10-28. See [volatile].
	if os.Getenv(volatile.Escape) == "" {
		if f := volatile.Check(p.ToolInput.Command, p.Cwd, os.Getenv("HOME"), os.Getenv("TMPDIR")); f.Found() {
			return deny(stdout, fmt.Sprintf(
				"Refused: %s — %s.\n\nIt matched %s\n\n%s",
				f.Dir, f.Why, f.Match, volatile.Advice))
		}
		// ⛔ A BACKGROUND COMMAND'S OUTPUT, the sixth scratchpad loss: on
		// 2026-11-11 a 64-minute `gotest … > <scratchpad>/probe.txt 2>&1` lost
		// its log when the session restarted. Foreground files stay allowed.
		if f := volatile.CheckRedirect(p.ToolInput.Command, p.Cwd, os.Getenv("HOME"), os.Getenv("TMPDIR"), p.ToolInput.RunInBackground); f.Found() {
			return deny(stdout, fmt.Sprintf(
				"Refused (volatile redirect): %s — %s.\n\nIt matched %s\n\n%s",
				f.Dir, f.Why, f.Match, volatile.RedirectAdvice))
		}
	}
	// ⛔ A pipeline's status is its LAST stage's. On 2026-10-11 this shape was
	// written three times in one afternoon — after `go build | head`, after
	// `go build | tail`, and after a fleet scan piped into `tail -40` — and each
	// time printed status=0 over a failed build and a scan that had found 350
	// carriers. There is a memory for it, re-read between two of the three. See
	// [pipestatus].
	if os.Getenv("GITSAFE_ALLOW_PIPE_STATUS") == "" {
		if f := pipestatus.Check(p.ToolInput.Command); f.Found() {
			return deny(stdout, fmt.Sprintf(
				"Refused: %s.\n\n%s\n\n%s",
				f.Why(), f.String(), pipestatus.Advice))
		}
	}
	// ⛔ `git checkout -b work origin/main` reads as "branch from main" and
	// branches from whatever this clone last fetched. On 2026-11-07 one session
	// did it twice with the lesson written down between the two: once orphaning
	// a documentation branch, once sending three pull requests against a base
	// Renovate had moved an hour earlier. Everything downstream was green both
	// times, and the only thing that said otherwise was GitHub's CONFLICTING,
	// after the build, the push and the pull request. The refusal belongs at the
	// branch, which is the last moment it costs nothing. See [stalebase].
	if os.Getenv(stalebase.Escape) == "" {
		if f := stalebase.Check(p.ToolInput.Command); f.Found() {
			return deny(stdout, fmt.Sprintf(
				"Refused: %s.\n\nIt matched %s\n\n%s",
				f.Why, f.Match, stalebase.Advice(f.Ref)))
		}
	}
	// ⛔ The SIBLING damage of the one above, and separable from it: a consumer
	// that stops reading kills the producer, so the work is ABANDONED rather
	// than merely mis-reported. On 2026-09-06 `timeout 400 go mod tidy 2>&1 |
	// head -5` was written up as a defect in a third party's module; re-run
	// without the pipe it exited 0 and the repository was fine. See [sigpipe].
	if os.Getenv("GITSAFE_ALLOW_SIGPIPE") == "" {
		if f := sigpipe.Check(p.ToolInput.Command); f.Found() {
			return deny(stdout, fmt.Sprintf(
				"Refused: %s.\n\n%s\n\n%s",
				f.Why(), f.String(), sigpipe.Advice))
		}
	}
	// ⛔ THE INSTRUCTION FILE AUTHORISES `gh`, AND THAT IS THE HOLE THIS CLOSES.
	// "Bash only invokes programs (go, git, gh, gitpush, …)" reads as leave to
	// type any `gh`, so on 2026-11-08 one comment on one issue cost two refusals
	// and a round trip to the user — while `ghissue`, purpose-built and already
	// installed, posted it first try. "Look for an existing tool first" was
	// written in that same file and in a memory on that exact subject, and
	// neither fired, because nothing RAN them.
	//
	// It goes before [shellwork] deliberately: both could refuse the same
	// command, and the specific advice — the name of the program to run — is
	// worth more than the general one. See [ghbyhand].
	// ⛔ NO os.Getenv WRAPPER, UNLIKE ITS NEIGHBOURS ABOVE. This hook runs
	// BEFORE any shell, so an escape typed in front of the command has not been
	// assigned to anything when the guard reads it: [ghbyhand.Escape] is read
	// from the command's own text, the way [volatile] and [bypass] read theirs.
	// Measured with hookcheck on the installed v0.37.0 binary, which answered
	// "refuse" to the escape row the corpus says to allow.
	//
	// ⛔ A WRITE NO PROGRAM COVERS IS REFUSED TOO, since 2026-11-08: it used to
	// pass, so `gh issue create` was typed by hand three times. The refusal
	// then asks for the missing tool rather than naming one.
	if f := ghbyhand.Check(p.ToolInput.Command); f.Found() {
		if f.Missing {
			return deny(stdout, fmt.Sprintf(
				"Refused: %s.\n\n%s\n\nThere is no program to use instead: %s",
				f.Why(), f.String(), f.Instead))
		}
		return deny(stdout, fmt.Sprintf(
			"Refused: %s.\n\n%s\n\nUse instead: %s\n\n%s",
			f.Why(), f.String(), f.Instead, ghbyhand.Advice))
	}
	// ⛔⛔ A FILTERED TEST RUN WHOSE FILTER NOBODY CHECKS. `ok` is about the
	// tests that RAN: on 2026-11-09 `go test -run 'TestREADME|TestUsage|TestMain'`
	// printed `ok … 0.159s` while `TestREADME` matched nothing, the other two
	// alternatives matched four tests, and the README gate being checked had not
	// run at all. Go warns only when the selection is WHOLLY empty, and exits 0
	// even then. See [testrun].
	//
	// ⛔ NO ESCAPE, per the direction recorded in v0.37.1 for [ghbyhand]: the
	// escape it announced was removed rather than fixed. `gotest` takes the same
	// arguments and runs the same command, so there is nothing an escape would
	// buy except the defect.
	//
	// Before [shellwork] because it is the specific rule and that one is the
	// general one — though shellwork does not cover this at all, since
	// `go test ./...` is invoking a tool and is exactly what it allows.
	if f := testrun.Check(p.ToolInput.Command); f.Found() {
		return deny(stdout, fmt.Sprintf(
			"Refused: %s.\n\n%s\n\nUse instead: %s\n\n%s",
			f.Why(), f.String(), f.Instead, testrun.Advice))
	}

	// ⛔⛔ A GATE AND THE WRITE IT GATES, IN ONE COMMAND LINE. See [chainedgate].
	//
	// This one is not about forgetting to run the check. The check RAN, printed
	// its refusal, and the pull request was created by the next word on the same
	// line — three times on 2026-11-09 and 2026-11-10, twice with the refusal
	// read and acted on one command too late. A `;` leaves no moment in which a
	// verdict can change anything.
	//
	// ⛔ `&&` IS ALLOWED, and that is what makes this a rule rather than an
	// obstacle: it is the spelling in which the shell itself enforces the
	// verdict. A rule with no correct form gets routed around.
	if f := chainedgate.Check(p.ToolInput.Command); f.Found() {
		return deny(stdout, fmt.Sprintf(
			"Refused: %s.\n\n%s\n\nUse instead: %s\n\n%s",
			f.Why(), f.String(), f.Instead, chainedgate.Advice))
	}
	// ⛔⛔ THE WRITE WITH NO GATE AT ALL. The rule above refuses a check and its
	// write joined by `;`; it cannot see a `gh pr create` that ran no check.
	// Five pull requests written by agents on 2026-11-12 were refused by ghmerge
	// at merge time for a bare issue reference, each rewritten by hand at the last
	// step, with prbody installed throughout and nothing running it. A pull
	// request from a body file now needs prbody AND paritycheck text on that
	// file, `&&`-joined before it — and the refusal gives the line to type.
	if m := chainedgate.Require(p.ToolInput.Command); m.Found() {
		return deny(stdout, fmt.Sprintf(
			"Refused: %s.\n\n%s\n\nType this instead: %s\n\n%s",
			m.Why(), m.String(), m.Instead(), chainedgate.RequiredAdvice))
	}

	// ⛔ A backtick is a code fence to every reader and a command substitution
	// to the shell, and the collision is silent: the words are gone, the
	// substituted command RAN, and the tool that received the text answered
	// success. An `agentsync tell` whose message quoted two command names in
	// backticks did exactly that three times in one session on 2026-11-07/08 —
	// the third time inside a message explaining the first two. The lesson was
	// written down after the first and recurred twice, which is this fleet's
	// test for when a note has to become a refusal. See [literaltick].
	//
	// ⛔ NO os.Getenv WRAPPER, for the reason [ghbyhand] records above: this
	// hook runs BEFORE any shell, so an escape typed in front of the command
	// has not been assigned to anything when the guard reads it. The first
	// version of this rule wrapped it in os.Getenv and its corpus row passed
	// anyway — against an installed binary that did not have the rule at all,
	// which is a row passing for the wrong reason. The escape is read from the
	// command's own text, the way [volatile] and [bypass] read theirs.
	if f := literaltick.Check(p.ToolInput.Command); f.Found() {
		return deny(stdout, fmt.Sprintf(
			"Refused: %s.\n\n%s\n\n%s",
			f.Why(), f.String(), literaltick.Advice))
	}
	// ⛔ THE INSTRUCTION FILE'S SECOND SECTION, which until now lived only
	// there: "No awk, sed, shell, python or perl — write the tool in Go …
	// Bash only invokes tools … no pipes, loops".
	//
	// 2026-10-18 is the control for why it belongs here. The one rule of this
	// family that was already a guard — pipestatus, above — refused the
	// operator at 09:35 and could not be worked around. This one, written down
	// three times in the same file and agreed to twice, was broken for twelve
	// hours, and cost five wrong measurements in one session. See [shellwork].
	//
	// It is the broadest rule here, which is why its own allow table is the
	// longer one and why the escape exists.
	if os.Getenv("GITSAFE_ALLOW_SHELLWORK") == "" {
		if f := shellwork.Check(p.ToolInput.Command); f.Found() {
			return deny(stdout, fmt.Sprintf(
				"Refused: %s.\n\n%s\n\n%s\n\n%s",
				f.Why(), f.String(), f.Instead, shellwork.Advice))
		}
	}
	return 0
}

// deny writes the harness's deny decision and returns 0 — the exit code says
// whether the guard itself worked, not whether it allowed the command.
func deny(stdout io.Writer, reason string) int {
	var d decision
	d.HookSpecificOutput.HookEventName = "PreToolUse"
	d.HookSpecificOutput.PermissionDecision = "deny"
	d.HookSpecificOutput.PermissionDecisionReason = reason
	out, err := json.Marshal(d)
	if err != nil {
		return 0
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}
