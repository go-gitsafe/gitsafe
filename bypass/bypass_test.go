package bypass

import (
	"strings"
	"testing"
)

// TestTheThreeAreRefused. Each of these is a standing instruction that was
// read, understood, and broken anyway.
func TestTheThreeAreRefused(t *testing.T) {
	for _, tc := range []struct{ cmd, rule string }{
		{"git push origin main", "push"},
		{"git push", "push"},
		{"cd /x && git push --force-with-lease origin b", "push"},
		{"git -c core.pager=cat push origin main", "push"},
		{"gh pr merge 42", "merge"},
		{"gh pr merge --squash 42", "merge"},
		{"cd x; gh pr merge", "merge"},
		{"git -c user.email=me@example.com commit -m x", "identity"},
		{"git -c user.name=Me commit", "identity"},
		// Both at once: the identity is the one that rewrites history, so it
		// is the one named.
		{"git -c user.email=me@example.com push", "identity"},
	} {
		f := Check(tc.cmd)
		if !f.Found() {
			t.Errorf("allowed: %s", tc.cmd)
			continue
		}
		if f.Rule != tc.rule {
			t.Errorf("rule = %q, want %q, for: %s", f.Rule, tc.rule, tc.cmd)
		}
		if f.Advice == "" {
			t.Errorf("a refusal that does not say what to do instead is an obstacle: %s", tc.cmd)
		}
	}
}

// TestWhatMustSTAYAllowed is the half that decides whether this guard
// survives. A guard that refuses harmless commands is one people learn to work
// around, and then it protects nothing at all.
//
// The quoted heredocs are not hypothetical: the shell rules this replaces
// refused four of them in one session on 2026-09-29, while their author was
// writing tests ABOUT the commands they forbid.
func TestWhatMustSTAYAllowed(t *testing.T) {
	for _, cmd := range []string{
		// Writing about the forbidden form. The body of a heredoc whose tag is
		// quoted is never expanded by the shell, so it is not the command.
		"cat <<'EOF'\ngit push origin main\nEOF",
		"cat > f.md <<'DOC'\nUse `gh pr merge` only through ghmerge.\nDOC",
		"python3 - <<'PY'\ns = 'git -c user.email=x commit'\nPY",
		// The wrappers themselves.
		"gitpush origin main",
		"ghmerge 42",
		"ghmerge owner/repo 42",
		// git subcommands that merely contain the word.
		"git stash push",
		"git stash push -m 'wip'",
		"git log --oneline",
		"git commit -m 'mention git push in the message'",
		// gh commands that are not a merge.
		"gh pr list",
		"gh pr view 42",
		"gh pr merge-queue status",
		// A path or a name that ends in the word.
		"./scripts/git-push-all.sh",
		"cat notes-about-git-push.txt",
		// An identity read rather than set.
		"git config user.email",
		"git config --get user.name",
		// The deliberate escape.
		Escape + "=1 gh pr merge 42",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("refused, and it must not be: %s\n  rule=%s match=%q", cmd, f.Rule, f.Match)
		}
	}
}

// The escape is deliberate and says so. It covers the merge only — there is no
// escape for pushing with plain git, because the wrapper takes the same
// arguments and there is nothing it cannot do.
func TestTheEscapeCoversTheMergeOnly(t *testing.T) {
	if f := Check(Escape + "=1 gh pr merge 42"); f.Found() {
		t.Errorf("the escape did not work: %s", f.Rule)
	}
	if f := Check(Escape + "=1 git push origin main"); !f.Found() || f.Rule != "push" {
		t.Errorf("there is no escape for a plain push, and there should not be: %v", f)
	}
}

// A refusal points at what it matched, so a reader can see it rather than be
// told.
func TestARefusalNamesWhatItMatched(t *testing.T) {
	f := Check("cd /tmp && git push --force origin main")
	if f.Match == "" || !strings.Contains(f.Match, "push") {
		t.Errorf("match = %q", f.Match)
	}
	if f.Why == "" {
		t.Error("a refusal with no reason is an obstacle rather than a rule")
	}
}

// The shapes the first corpus did not reach: an option-joined identity, a
// segment that is nothing but an assignment, git and gh with no subcommand at
// all, and gh with its own options before `pr`.
func TestTheEdgesOfTheParse(t *testing.T) {
	for _, tc := range []struct{ cmd, rule string }{
		// `-cuser.email=x`, joined, which git accepts.
		{"git -cuser.email=me@example.com commit", "identity"},
		// The subcommand's own options, which come AFTER it: gh's only global
		// flags are --help and --version, checked against `gh --help`, so
		// `gh --repo o/r pr merge` is not something anyone can type.
		{"gh pr merge --repo o/r 42", "merge"},
		// --help merges nothing. The shell rule refused this one too.
		{"gh pr merge --help", ""},
		{"gh pr merge -h", ""},
		// An assignment and nothing else: no command to judge.
		{"FOO=bar", ""},
		{"", ""},
		// A command with no subcommand.
		{"git", ""},
		{"gh", ""},
		{"gh pr", ""},
		// -C takes a value, and the value is not an identity.
		{"git -C /tmp/repo status", ""},
		{"git -C /tmp/repo push", "push"},
		// -c with a value that is not an identity.
		{"git -c core.pager=cat log", ""},
		// A trailing -c with nothing after it must not read past the end.
		{"git -c", ""},
		// Something else entirely in command position.
		{"echo git push", ""},
	} {
		f := Check(tc.cmd)
		if tc.rule == "" {
			if f.Found() {
				t.Errorf("refused, and it must not be: %q (rule=%s)", tc.cmd, f.Rule)
			}
			continue
		}
		if f.Rule != tc.rule {
			t.Errorf("rule = %q, want %q, for %q", f.Rule, tc.rule, tc.cmd)
		}
	}
}
