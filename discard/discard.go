// Package discard answers one question: would this command throw away work that
// exists only in the working tree?
//
// It exists because it happened three times, in three different spellings, and
// the written rule stopped none of them:
//
//   - `git checkout -q -- .` slipped into a chain that prepared a commit. It
//     erased the fix and the go.mod bump and left only the untracked test file.
//     The commit succeeded, the push succeeded, the pull request opened: every
//     signal was green and the content was wrong.
//   - `git checkout <branch> -- <file>` destroyed uncommitted tests twice in one
//     session; the repository's coverage gate is what forced them to be written
//     again.
//   - `git checkout main` in a background merge watcher switched branches out
//     from under an edit being written in the same repository, and the change and
//     its test vanished with the branch that had just been deleted.
//
// # What it refuses, and why it is stated as an ACT
//
// The rule names the act — discarding uncommitted work — rather than one
// spelling of it. A guard that refused `git checkout -- .` alone would leave
// `git restore .`, `git reset --hard` and `git clean -fd` doing the same damage,
// and whoever hit the refusal would reach for the next spelling rather than for
// `git stash`. That is how a list inherits its own blind spot.
//
// # What it does NOT refuse
//
// The restraint is the point rather than a concession, because a guard that
// refuses ordinary work is one people learn to route around:
//
//	git checkout -b a-branch     # switching and creating: git protects those itself
//	git checkout main            # and refuses when it would lose data
//	git restore --staged f.go    # unstages; the working tree is untouched
//	git reset --soft HEAD~       # moves the branch, keeps the tree
//	git stash push -- f.go       # the safe equivalent, which is what to say instead
package discard

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Rule names what matched, for a caller that wants to distinguish them.
	Rule string
	// Match is the fragment of the command that triggered it, so a refusal can
	// point at something rather than assert.
	Match string
	// Why is one sentence a person can act on.
	Why string
}

// Found reports whether anything was found.
func (f Finding) Found() bool { return f.Rule != "" }

// Check reports whether cmd would discard uncommitted work.
func Check(cmd string) Finding {
	for _, part := range segments(shellcmd.StripQuotedHeredocs(cmd)) {
		if f := checkOne(part); f.Found() {
			return f
		}
	}
	return Finding{}
}

// segments splits a command line on the separators that start a new command, so
// that a destructive git buried in a chain is judged too — which is exactly
// where the first one hid: `git checkout -q -- . 2>/dev/null; git status …`.
func segments(cmd string) []string {
	f := func(r rune) bool { return r == ';' || r == '&' || r == '|' || r == '\n' }
	return strings.FieldsFunc(cmd, f)
}

func checkOne(part string) Finding {
	fields := strings.Fields(part)
	// Find the git invocation: it may be preceded by env assignments or by a
	// wrapper that takes the same arguments.
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "-") {
		i++
	}
	if i >= len(fields) || base(fields[i]) != "git" {
		return Finding{}
	}
	args := fields[i+1:]
	// Skip git's own options (-C dir, -c key=value) to reach the subcommand.
	sub, rest := subcommand(args)
	switch sub {
	case "checkout":
		return checkCheckout(part, rest)
	case "restore":
		return checkRestore(part, rest)
	case "reset":
		if has(rest, "--hard") {
			return Finding{"reset-hard", "git reset --hard",
				"throws away every uncommitted change in the working tree, tracked and staged alike"}
		}
	case "clean":
		for _, a := range rest {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "fF") {
				return Finding{"clean-force", "git clean " + a,
					"deletes untracked files, which is where work that was never added lives"}
			}
			if a == "--force" {
				return Finding{"clean-force", "git clean --force",
					"deletes untracked files, which is where work that was never added lives"}
			}
		}
	}
	return Finding{}
}

// checkCheckout refuses the PATHSPEC form, which overwrites files from a commit,
// and leaves branch switching alone — git already refuses that when it would
// lose data, and switching branches is most of what checkout is for.
func checkCheckout(part string, args []string) Finding {
	// `git checkout -- <paths>` and `git checkout <ref> -- <paths>`
	for _, a := range args {
		if a == "--" {
			return Finding{"checkout-paths", "git checkout … -- …",
				"overwrites those paths from a commit, discarding whatever the working tree held"}
		}
	}
	// `git checkout .` — the same act without the separator.
	for _, a := range args {
		if a == "." {
			return Finding{"checkout-dot", "git checkout .",
				"overwrites every tracked file from HEAD, discarding whatever the working tree held"}
		}
	}
	return Finding{}
}

// checkRestore refuses the form that writes the working tree. `--staged` alone
// only unstages, which loses nothing.
func checkRestore(part string, args []string) Finding {
	staged, worktree, paths := false, false, false
	for _, a := range args {
		switch {
		case a == "--staged" || a == "-S":
			staged = true
		case a == "--worktree" || a == "-W":
			worktree = true
		case a == "--":
		case strings.HasPrefix(a, "-"):
		default:
			paths = true
		}
	}
	if !paths {
		return Finding{}
	}
	if staged && !worktree {
		return Finding{} // unstaging only
	}
	return Finding{"restore-worktree", "git restore …",
		"writes those paths back from a commit, discarding whatever the working tree held"}
}

// subcommand returns git's subcommand and the arguments after it, stepping over
// git's own options — `git -C dir`, `git -c key=value` — which take a value.
func subcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return a, args[i+1:]
		}
		if a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" {
			i++ // its value
		}
	}
	return "", nil
}

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// base is the command name without its directory, so /usr/bin/git reads as git.
func base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Advice is what to do instead, printed with a refusal.
const Advice = `Keep it instead of dropping it — a stash is recoverable and a discard is not:

    git stash push -- <paths>     # then git stash pop, or drop it later
    git diff > /tmp/keep.patch    # if it is going away for good

If the work really is unwanted, say so on purpose for one command:

    GITSAFE_ALLOW_DISCARD=1 <the command>`
