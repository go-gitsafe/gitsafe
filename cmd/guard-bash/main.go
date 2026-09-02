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

	"github.com/go-gitsafe/gitsafe/secretarg"
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
	f := secretarg.Check(p.ToolInput.Command)
	if !f.Found() {
		return 0
	}
	var d decision
	d.HookSpecificOutput.HookEventName = "PreToolUse"
	d.HookSpecificOutput.PermissionDecision = "deny"
	d.HookSpecificOutput.PermissionDecisionReason = fmt.Sprintf(
		"Refused: %s.\n\nIt matched %s\n\n%s\n\nIf the value is NOT a secret, name the file something that does not read as one.",
		f.Why, f.Match, secretarg.Advice)
	out, err := json.Marshal(d)
	if err != nil {
		return 0
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}
