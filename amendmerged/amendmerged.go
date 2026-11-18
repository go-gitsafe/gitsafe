// Copyright (c) the go-gitsafe authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package amendmerged answers one question: does this command amend a commit
// that is already on the remote's default branch?
//
// `git commit --amend` rewrites HEAD. When HEAD is the merge commit of the
// previous pull request — already on main, already published — the rewrite
// leaves local main one commit that no remote has, and whatever was staged is
// folded into somebody else's change.
//
// # How it happened, twice
//
// 2026-11-16 and 2026-11-18: a chained `git add && git commit -F msg && gitpush`
// was refused by ANOTHER pre-tool hook. A refusal happens before any part of the
// chain runs, the commit included, so nothing had been committed. The retry was
// written from memory of what the first command did:
//
//	go mod tidy && git add … && git commit --amend --no-edit
//
// and amended the previous pull request's merge commit. Both times it was caught
// only because `git show --stat` printed the other PR's subject. The written
// lesson did not prevent the second; the refusal below arrives at the amend.
//
// # Unlike its neighbours, this rule reads the repository
//
// The textual rules (see [stalebase]) cannot know whether HEAD is published; this
// one asks git: `git merge-base --is-ancestor HEAD origin/<default>`. It resolves
// the clone the way the other rules do: -C, an earlier `cd`, else the cwd.
//
// # What it does NOT refuse
//
// A refusal is not a verdict, and ~25 sessions share the machine, so every doubt
// allows: no repository, no origin, no origin/<default>, an unreadable repo, a
// git that fails or takes longer than a few seconds. It also allows:
//
//	amend of an unpushed commit on a topic branch      # the author's own
//	amend of a pushed but unmerged topic-branch commit # still the author's own
//	git commit -m x                                    # no --amend
//	git commit -m x && git commit --amend              # HEAD changed first
//	git commit -m "mention --amend"                    # inside quotes
//	GITSAFE_ALLOW_AMEND_MERGED=1 git commit --amend    # said on purpose
package amendmerged

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Escape is the assignment that says amending a published commit is meant. It is
// honoured as a PREFIX ON THE COMMAND, because the guard reads the command as
// text and the environment the command would run with never reaches its process.
const Escape = "GITSAFE_ALLOW_AMEND_MERGED"

// timeout bounds each git call; past it the answer is "cannot check", so allow.
const timeout = 5 * time.Second

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Dir is the clone the amend would run in.
	Dir string
	// Commit is the short sha of HEAD, Subject its subject line.
	Commit, Subject string
	// Default is the default branch HEAD is already on, such as "main".
	Default string
}

// Found reports whether anything was found.
func (f Finding) Found() bool { return f.Commit != "" }

// Why is one sentence a person can act on.
func (f Finding) Why() string {
	return "`git commit --amend` would rewrite " + f.Commit + " (" + f.Subject +
		"), which is already on " + f.Default + " — the previous change's commit, not yours"
}

// Advice is what to do instead, printed with a refusal.
const Advice = `Make a new commit instead: a plain ` + "`git commit`" + ` (with -F <file> or -m).
Check what HEAD is before touching it:

    git log -1

If the refused command was a chain, it ran NOTHING: the earlier steps did not
happen either. Re-run them as they were.

If amending a published commit is really meant, say so on purpose for one command:

    ` + Escape + `=1 <the command>`

// Check reports whether cmd, run in cwd, amends a commit already on the
// default branch of its clone. home is $HOME, for `cd ~/x`.
func Check(cmd, cwd, home string) Finding {
	return check(cmd, cwd, home, gitInspect)
}

// inspector answers for one directory: HEAD's short sha, subject and the default
// branch name when HEAD is reachable from origin/<default>; ok is false when it
// is not, or when that cannot be told.
type inspector func(dir string) (commit, subject, def string, ok bool)

func check(cmd, cwd, home string, inspect inspector) Finding {
	if !strings.Contains(cmd, "--amend") {
		return Finding{} // the common case costs no git call
	}
	if cwd == "" {
		cwd = "."
	}
	dir := cwd
	for _, st := range shellcmd.Statements(shellcmd.StripQuotedHeredocs(cmd)) {
		words := splitWords(st)
		i := 0
		for i < len(words) && strings.Contains(words[i], "=") && !strings.HasPrefix(words[i], "-") {
			if name, value, _ := strings.Cut(words[i], "="); name == Escape && value != "" && value != "0" {
				return Finding{}
			}
			i++
		}
		words = words[i:]
		if len(words) == 0 {
			continue
		}
		if base(words[0]) == "cd" {
			if len(words) == 1 {
				dir = home
			} else {
				dir = resolve(dir, home, words[1])
			}
			continue
		}
		if base(words[0]) != "git" {
			continue
		}
		at, sub, rest := subcommand(words[1:], dir, home)
		if movesHead(sub) && !(sub == "commit" && hasAmend(rest)) {
			// HEAD is not what it is now by the time a later amend runs.
			return Finding{}
		}
		if sub != "commit" || !hasAmend(rest) {
			continue
		}
		if c, s, d, ok := inspect(at); ok {
			return Finding{Dir: at, Commit: c, Subject: s, Default: d}
		}
	}
	return Finding{}
}

// movesHead lists the subcommands that change what HEAD is, so a later amend in
// the same command is about the new HEAD and this rule cannot judge it.
func movesHead(sub string) bool {
	switch sub {
	case "commit", "cherry-pick", "merge", "revert", "pull", "rebase", "reset",
		"checkout", "switch", "am", "clone", "init":
		return true
	}
	return false
}

// hasAmend reports whether a commit's arguments carry --amend. Words came out of
// splitWords, so a mention inside a quoted message is not a word of its own.
func hasAmend(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--amend" {
			return true
		}
	}
	return false
}

// subcommand returns the directory git works in (dir moved by -C), its
// subcommand and the arguments after it.
func subcommand(args []string, dir, home string) (string, string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" && i+1 < len(args):
			dir = resolve(dir, home, args[i+1])
			i++
		case a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace":
			i++
		case !strings.HasPrefix(a, "-"):
			return dir, a, args[i+1:]
		}
	}
	return dir, "", nil
}

// splitWords splits a statement on whitespace, honouring '…', "…" and backslash,
// and returns the words with their quotes removed.
func splitWords(s string) []string {
	var out []string
	var cur strings.Builder
	in := false // a word is open (an empty "" still counts)
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q == '\'':
			if c == '\'' {
				q = 0
			} else {
				cur.WriteByte(c)
			}
		case q == '"':
			switch {
			case c == '"':
				q = 0
			case c == '\\' && i+1 < len(s):
				i++
				cur.WriteByte(s[i])
			default:
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			q, in = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

// resolve makes p absolute against dir, expanding a leading ~ or $HOME.
func resolve(dir, home, p string) string {
	switch {
	case p == "~" || p == "$HOME":
		return home
	case strings.HasPrefix(p, "~/"):
		return joinPath(home, p[2:])
	case strings.HasPrefix(p, "$HOME/"):
		return joinPath(home, p[6:])
	case strings.HasPrefix(p, "/"):
		return p
	}
	return joinPath(dir, p)
}

func joinPath(a, b string) string { return strings.TrimRight(a, "/") + "/" + b }

func base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// gitInspect asks git. Every failure is "cannot check".
func gitInspect(dir string) (commit, subject, def string, ok bool) {
	ref, def, found := defaultRef(dir)
	if !found {
		return "", "", "", false
	}
	// Exit 0: HEAD is an ancestor of (or is) the default branch. Exit 1: it is
	// not. Anything else — 128 for an unborn HEAD, a signal, a timeout — is a
	// failure to check, not a verdict.
	if _, code := git(dir, "merge-base", "--is-ancestor", "HEAD", ref); code != 0 {
		return "", "", "", false
	}
	out, code := git(dir, "log", "-1", "--format=%h%x00%s", "HEAD")
	if code != 0 {
		return "", "", "", false
	}
	commit, subject, _ = strings.Cut(strings.TrimSpace(out), "\x00")
	if commit == "" {
		return "", "", "", false
	}
	return commit, subject, def, true
}

// defaultRef resolves origin's default branch: origin/HEAD when set, else
// origin/main or origin/master if exactly the ref exists.
func defaultRef(dir string) (ref, name string, ok bool) {
	if out, code := git(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); code == 0 {
		if r := strings.TrimSpace(out); strings.HasPrefix(r, "origin/") {
			if _, c := git(dir, "rev-parse", "--verify", "--quiet", r+"^{commit}"); c == 0 {
				return r, strings.TrimPrefix(r, "origin/"), true
			}
		}
	}
	for _, n := range []string{"main", "master"} {
		if _, c := git(dir, "rev-parse", "--verify", "--quiet", "origin/"+n+"^{commit}"); c == 0 {
			return "origin/" + n, n, true
		}
	}
	return "", "", false
}

// git runs one read-only git command in dir and returns its stdout and exit
// code; a command that could not run or timed out answers -1.
func git(dir string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := c.Output()
	if err == nil {
		return string(out), 0
	}
	if ee, ok := err.(*exec.ExitError); ok && ctx.Err() == nil {
		return string(out), ee.ExitCode()
	}
	return "", -1
}
