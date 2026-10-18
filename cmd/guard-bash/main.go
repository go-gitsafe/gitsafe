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
	"github.com/go-gitsafe/gitsafe/discard"
	"github.com/go-gitsafe/gitsafe/pipestatus"
	"github.com/go-gitsafe/gitsafe/secretarg"
	"github.com/go-gitsafe/gitsafe/shellwork"
	"github.com/go-gitsafe/gitsafe/sigpipe"
)

// payload is the part of the PreToolUse event this reads.
type payload struct {
	ToolInput struct {
		Command string `json:"command"`
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
