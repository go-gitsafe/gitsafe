// Copyright (c) the go-gitsafe authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package stalebase answers one question: does this command start a branch from
// a remote-tracking ref it has not refreshed?
//
// `git checkout -b work origin/main` reads as "branch from main". It does not:
// it branches from whatever `origin/main` pointed at the last time this clone
// fetched, which in a repository several sessions push to is a commit that
// stopped being main some time ago. Nothing complains. The branch is created,
// the code builds, the tests pass, the push succeeds and the pull request
// opens — and then GitHub says CONFLICTING, or worse, says nothing and the
// checks that run are the ones for a base nobody is on any more.
//
// # Why this is a refusal and not a note
//
// It happened twice in one session with the lesson already written down between
// the two — once leaving an orphaned documentation branch, once sending three
// pull requests out against a base Renovate had moved an hour earlier. The
// written rule ("rebase onto origin/main, and check the checkout succeeded")
// was read, and then not applied, because nothing asks at the moment the branch
// is created. The refusal arrives there: before the build, the push, the pull
// request and the reviewer.
//
// # The one heuristic, and why it is sound HERE
//
// This rule cannot look at the repository — it is a pre-tool hook that receives
// the command as TEXT, with no clone to inspect and no network. So it asks a
// textual question instead: does this command line also fetch?
//
//	git fetch origin && git checkout -b work origin/main   # allowed
//	git checkout -b work origin/main                       # refused
//
// In an interactive shell that would be a poor proxy, because a person fetches,
// reads the log, thinks, and branches three commands later. ⛔ But this guard
// hooks an AGENT's tool calls, and an agent's commands are self-contained: each
// one is issued without memory of the shell state the last one left. Measured
// on the session that prompted this: every branch created correctly carried its
// own `git fetch` in the same command, and every stale one had no fetch
// anywhere near it. The proxy matches the way the commands are actually
// written.
//
// # What it does NOT refuse
//
// The restraint is the point, because a guard that refuses ordinary work is one
// people learn to route around, and a derogation that becomes the normal route
// is no longer a derogation:
//
//	git checkout -b work                 # from HEAD; no remote ref is named
//	git checkout -b work some-local-ref  # a local base is as fresh as the clone
//	git checkout -b work v1.2.3          # a TAG does not move
//	git switch -c work                   # same, in the modern spelling
//	git checkout main                    # switching, not creating
//	git fetch && git switch -c work origin/main   # already correct
package stalebase

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Escape is the assignment that says an unrefreshed base is meant — branching
// from a known older commit, or working with no network. It is honoured as a
// PREFIX ON THE COMMAND, because the guard reads the command as text and the
// environment the command would run with never reaches the guard's process.
const Escape = "GITSAFE_ALLOW_STALE_BASE"

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Rule names what matched.
	Rule string
	// Match is the fragment that triggered it, so a refusal points at
	// something rather than asserting.
	Match string
	// Why is one sentence a person can act on.
	Why string
	// Ref is the remote-tracking ref the branch would start from, so the
	// advice can name the fetch that would refresh it.
	Ref string
}

// Found reports whether anything was found.
func (f Finding) Found() bool { return f.Rule != "" }

// Check reports whether cmd creates a branch from a remote-tracking ref without
// refreshing it in the same command.
func Check(cmd string) Finding {
	clean := shellcmd.StripQuotedHeredocs(cmd)
	// ⛔ The fetch is looked for across the WHOLE command, not only in the
	// segments before the branch: `git checkout -b x origin/main` after a
	// `git fetch` in the same chain is correct however the two are ordered by
	// the shell, and a rule that insisted on the order would refuse
	// `git fetch origin; git switch -c x origin/main` written the other way
	// round for no gain.
	if fetches(clean) {
		return Finding{}
	}
	for _, part := range segments(clean) {
		if f := checkOne(part); f.Found() {
			return f
		}
	}
	return Finding{}
}

// fetches reports whether any segment of the command refreshes refs. `git pull`
// counts: it fetches before it merges.
func fetches(cmd string) bool {
	for _, part := range segments(cmd) {
		args, ok := gitArgs(part)
		if !ok {
			continue
		}
		switch sub, _ := subcommand(args); sub {
		case "fetch", "pull", "remote":
			return true
		}
	}
	return false
}

func segments(cmd string) []string {
	f := func(r rune) bool { return r == ';' || r == '&' || r == '|' || r == '\n' }
	return strings.FieldsFunc(cmd, f)
}

// checkOne judges one segment.
func checkOne(part string) Finding {
	args, ok := gitArgs(part)
	if !ok {
		return Finding{}
	}
	sub, rest := subcommand(args)

	var name string
	switch sub {
	case "checkout":
		// Creating: -b or -B, followed by the new name and an optional base.
		rest, ok = afterCreateFlag(rest, "-b", "-B")
		if !ok {
			return Finding{}
		}
	case "switch":
		rest, ok = afterCreateFlag(rest, "-c", "-C", "--create", "--force-create")
		if !ok {
			return Finding{}
		}
	case "branch":
		// `git branch <name> <base>`: creating without switching.
	default:
		return Finding{}
	}

	// The operands are the new name and, optionally, the base.
	ops := operands(rest)
	if len(ops) < 2 {
		return Finding{} // no base named: it starts from HEAD
	}
	name, base := ops[0], ops[1]
	_ = name
	if !isRemoteTracking(base) {
		return Finding{}
	}
	return Finding{
		Rule:  "stale-base",
		Match: "git " + sub + " … " + base,
		Ref:   base,
		Why: "starts a branch from " + base + " as this clone last saw it, which in a " +
			"repository several sessions push to is a commit that stopped being the tip " +
			"some time ago — and nothing downstream says so until the pull request conflicts",
	}
}

// afterCreateFlag reports whether one of the create flags is present and
// returns the arguments with that flag removed.
func afterCreateFlag(args []string, flags ...string) ([]string, bool) {
	for i, a := range args {
		for _, f := range flags {
			if a == f {
				return append(append([]string{}, args[:i]...), args[i+1:]...), true
			}
		}
	}
	return nil, false
}

// operands are the non-flag arguments, which for these subcommands are the new
// branch name and its optional starting point.
func operands(args []string) []string {
	var out []string
	for _, a := range args {
		if a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// isRemoteTracking reports whether a starting point names a remote-tracking
// ref, which is the thing a fetch refreshes.
//
// ⛔ A TAG IS NOT ONE even though it looks like a ref: a tag does not move, so
// branching from one is reproducible whether or not the clone is current. The
// form that matters is <remote>/<branch>, and `refs/remotes/...` spelled out.
func isRemoteTracking(base string) bool {
	if strings.HasPrefix(base, "refs/remotes/") {
		return true
	}
	if strings.HasPrefix(base, "refs/") {
		return false // refs/tags/..., refs/heads/... are local or immovable
	}
	remote, rest, found := strings.Cut(base, "/")
	if !found || remote == "" || rest == "" {
		return false
	}
	// A path-looking base with more than one slash is still a remote ref when
	// the branch itself has slashes: origin/renovate/go-1.x.
	return true
}

// gitArgs returns the arguments of a git invocation in this segment, stepping
// over leading environment assignments, and reports whether the segment is one.
// The escape is honoured here, on the very command that carries it.
func gitArgs(part string) ([]string, bool) {
	fields := strings.Fields(part)
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "-") {
		if name, value, _ := strings.Cut(fields[i], "="); name == Escape && value != "" && value != "0" {
			return nil, false // said on purpose
		}
		i++
	}
	if i >= len(fields) || base(fields[i]) != "git" {
		return nil, false
	}
	return fields[i+1:], true
}

// subcommand returns git's subcommand and what follows, stepping over git's own
// options, which may take a value.
func subcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return a, args[i+1:]
		}
		if a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" {
			i++
		}
	}
	return "", nil
}

func base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Advice is what to do instead, printed with a refusal.
func Advice(ref string) string {
	remote := "origin"
	if r, _, found := strings.Cut(ref, "/"); found && r != "" {
		remote = r
	}
	return `Refresh the ref in the same command, so the base is the tip and not a memory of it:

    git fetch ` + remote + ` && git checkout -b <name> ` + ref + `

If a branch is already on a stale base, the same fetch makes the rebase possible:

    git fetch ` + remote + ` && git rebase ` + ref + `

If an older base is meant — a backport, or no network — say so on purpose for
one command:

    ` + Escape + `=1 <the command>`
}
