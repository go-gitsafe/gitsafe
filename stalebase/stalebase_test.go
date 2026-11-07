// Copyright (c) the go-gitsafe authors.
// SPDX-License-Identifier: BSD-3-Clause

package stalebase

import (
	"strings"
	"testing"
)

// TestItRefusesTheShapeThatBitTwice.
//
// ⛔ The first two cases are verbatim from the session that prompted this rule:
// the first orphaned a documentation branch, the second sent three pull requests
// against a base Renovate had moved an hour earlier. Everything downstream was
// green both times.
func TestItRefusesTheShapeThatBitTwice(t *testing.T) {
	for _, cmd := range []string{
		"git checkout -b bomb-gate origin/main",
		"cd /repo && git checkout -b use-the-gate origin/main",
		"git switch -c work origin/main",
		"git switch --create work origin/main",
		"git checkout -B work origin/main",
		"git branch work origin/main",
		"git checkout -b fix origin/renovate/go-1.x",       // a branch with slashes
		"git checkout -b fix upstream/develop",             // any remote, not just origin
		"git checkout -b fix refs/remotes/origin/main",     // spelled out
		"git -C /elsewhere checkout -b fix origin/main",    // git's own options skipped
		"git checkout -b fix origin/main && go test ./...", // buried in a chain
	} {
		f := Check(cmd)
		if !f.Found() {
			t.Errorf("not refused: %s", cmd)
			continue
		}
		if f.Rule != "stale-base" {
			t.Errorf("%s: rule %q", cmd, f.Rule)
		}
		if f.Ref == "" {
			t.Errorf("%s: the finding does not name the ref, so the advice cannot name the fetch", cmd)
		}
	}
}

// TestItStaysShutOnOrdinaryWork is the witness.
//
// ⛔ A GUARD'S TEST HAS TO SHOW IT DOES NOT FIRE, or it proves only that it
// refuses everything — and a guard that refuses ordinary work is one people
// learn to route around, which is worse than no guard because the routing
// becomes habit.
func TestItStaysShutOnOrdinaryWork(t *testing.T) {
	for _, cmd := range []string{
		// Already correct: the fetch is in the same command.
		"git fetch origin && git checkout -b work origin/main",
		"git fetch && git switch -c work origin/main",
		"git fetch origin; git branch work origin/main",
		"git pull && git checkout -b work origin/main", // pull fetches
		// No remote ref named.
		"git checkout -b work",
		"git switch -c work",
		"git checkout -b work some-local-branch",
		"git checkout -b work HEAD~3",
		// A tag does not move, so a stale clone branches from the same commit.
		"git checkout -b work v1.2.3",
		"git checkout -b work refs/tags/v1.2.3",
		// Not creating anything.
		"git checkout main",
		"git switch main",
		"git checkout -- file.go",
		"git branch -d old",
		"git branch --list",
		// Not git at all.
		"gh pr create --base main",
		"echo git checkout -b work origin/main",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("refused ordinary work: %s\n  rule=%s match=%s", cmd, f.Rule, f.Match)
		}
	}
}

// TestTheEscapeIsHonouredOnTheCommandItself.
//
// ⛔ As a PREFIX, because the guard runs as a pre-tool hook and reads the
// command as text: the environment the command would run with never reaches the
// guard's process. Documented the other way round, the escape would open
// nothing — and a refusal that names a way out which does not work is worse
// than one that names none.
func TestTheEscapeIsHonouredOnTheCommandItself(t *testing.T) {
	if f := Check(Escape + "=1 git checkout -b backport origin/main"); f.Found() {
		t.Error("the escape did not open the refusal")
	}
	// And it must not open on a value that reads as off.
	for _, v := range []string{"", "0"} {
		if f := Check(Escape + "=" + v + " git checkout -b work origin/main"); !f.Found() {
			t.Errorf("%s=%q opened the refusal", Escape, v)
		}
	}
}

// TestTheAdviceNamesTheRemoteItSaw, so the fetch it suggests is runnable rather
// than a template to adapt.
func TestTheAdviceNamesTheRemoteItSaw(t *testing.T) {
	f := Check("git checkout -b fix upstream/develop")
	if !f.Found() {
		t.Fatal("not refused")
	}
	a := Advice(f.Ref)
	if !strings.Contains(a, "git fetch upstream") {
		t.Errorf("the advice does not name the remote it saw:\n%s", a)
	}
	if !strings.Contains(a, "upstream/develop") {
		t.Errorf("the advice does not name the ref:\n%s", a)
	}
	if !strings.Contains(a, Escape) {
		t.Errorf("the advice does not name the escape:\n%s", a)
	}
}

// TestADiscussionOfACommandIsNotACommand: the rule reads text, and text about a
// command is the classic false refusal — six of ten cases on the shell script
// this package's siblings replaced were exactly that.
func TestADiscussionOfACommandIsNotACommand(t *testing.T) {
	for _, cmd := range []string{
		`echo "use git checkout -b work origin/main"`,
		`printf 'git switch -c x origin/main\n'`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("refused a command that only MENTIONS one: %s", cmd)
		}
	}
}
