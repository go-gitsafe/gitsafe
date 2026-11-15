// Package emptypush answers one question: is this push about to publish a
// branch that carries no commit the remote does not already have?
//
// # Why it exists
//
// Twice, thirteen days apart, a branch was published holding none of its
// author's work, and both times the output read as success.
//
// 2026-11-01:
//
//	git commit -m "… arg(s), …"; gitpush -u origin work
//
// The message contained `arg(s),`, zsh found no matching glob, and the commit
// never ran. The `;` ran the push anyway.
//
// 2026-11-15, go-ansible/modules:
//
//	git commit -q -F msg.txt && git log --oneline -1 && gitpush -u origin work
//
// This one obeyed the remedy written after the first — message in a file,
// `git commit -F`, joined by `&&` rather than `;`. It failed anyway: a
// PreToolUse hook refused the command, and a hook refusal does not fail the
// first word, it annihilates the whole line before any shell runs. Nothing was
// left for `&&` to short-circuit. The gate was then cleared and the push alone
// re-issued, because the commit had been invisible inside the refused call.
//
// # ⛔ Why the gate is HERE and not on the shape of the command
//
// The first attempt at this was a guard-bash rule refusing any command line
// that both committed and pushed. Running it over the whole transcript record
// measured what it would cost: **4137 runs, 3378 distinct commands, out of
// 594 557** — against two occurrences of the defect. A guard with that ratio
// does not protect a workflow, it replaces one, and this fleet has written down
// what happens next: a rule that refuses ordinary work gets routed around.
//
// The measurement also said where the gate belongs. Nothing is wrong with
// committing and pushing in one breath. What is wrong is that a push which
// sends no commits says
//
//   - [new branch]      work -> work
//
// and exits 0. Both occurrences are the same condition — every commit of HEAD
// was already on the remote — and that condition is one git question, true
// exactly when the harm is real.
//
// # What it applies to
//
// A plain push of the current branch, which is the shape both occurrences had.
// Anything that says otherwise — an explicit refspec with a colon, a different
// branch by name, `--delete`, `--all`, `--mirror`, `--tags` — is left alone:
// this cannot reason about what those publish, and a guard that guesses is
// worse than one that declines. Unreadable input means no opinion.
package emptypush

import "strings"

// Escape lets a deliberate pointer-only push through. It exists because
// creating a branch at an existing commit is occasionally what someone means,
// and a rule with no correct form gets worked around.
const Escape = "GITSAFE_ALLOW_EMPTY_PUSH"

// Advice is the remedy.
const Advice = "Nothing was published. If you meant to commit first, the commit " +
	"did not happen — `git log --oneline -1` names what HEAD actually points at, " +
	"and `git status --short` shows whether the work is merely staged. ⛔ After a " +
	"hook refusal, re-run the WHOLE command, never the part you believe is left: " +
	"the refusal ran none of it. To publish a branch pointer on purpose, say so: " +
	Escape + "=1 gitpush …"

// Applies reports whether this argument list is a plain push of the current
// branch, which is the only shape this rule judges.
//
// branch is the current branch name, empty on a detached HEAD — where this
// declines, since there is no branch being published by name.
func Applies(args []string, branch string) bool {
	if branch == "" {
		return false
	}
	var positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			// These publish or remove something this rule cannot reason about.
			switch a {
			case "--delete", "-d", "--all", "--mirror", "--tags", "--prune":
				return false
			}
			continue
		}
		positional = append(positional, a)
	}
	// positional[0] is the remote; anything after it is a refspec.
	if len(positional) > 2 {
		// Several refspecs at once: not the shape either occurrence had.
		return false
	}
	if len(positional) == 2 {
		ref := positional[1]
		if strings.Contains(ref, ":") {
			// An explicit mapping, which may push something other than HEAD.
			return false
		}
		if ref != branch {
			// A different branch by name; HEAD is not what is being published.
			return false
		}
	}
	return true
}

// Why is the refusal's first line, given the remote it would have pushed to.
func Why(remote string) string {
	return "this push would send NO commits: every commit of HEAD is already on " +
		remote + ", so it would publish a branch holding none of your work — " +
		"and git would report that as success"
}
