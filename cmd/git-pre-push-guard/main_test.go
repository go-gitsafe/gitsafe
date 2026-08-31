package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRefusesACredentialBearingURL is the whole job. The message must be
// actionable and must NOT repeat the secret — quoting a credential in order to
// complain about it is the mistake itself.
func TestRefusesACredentialBearingURL(t *testing.T) {
	const tok = "ghp_0123456789abcdefghijABCDEFGHIJ"
	for _, url := range []string{
		"https://x-access-token:" + tok + "@github.com/go-xrkit/desk.git",
		"https://user:hunter2hunter2@github.com/x/y.git",
	} {
		var out, errb bytes.Buffer
		if code := run([]string{"origin", url}, strings.NewReader(""), &out, &errb); code != 1 {
			t.Errorf("%s: exit %d, want 1", url, code)
		}
		msg := errb.String()
		if strings.Contains(msg, tok) || strings.Contains(msg, "hunter2") {
			t.Errorf("the refusal repeated the secret: %s", msg)
		}
		for _, want := range []string{"refusing to push", "set-url", "gitpush"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal does not mention %q: %s", want, msg)
			}
		}
	}
}

// TestLetsACleanPushThrough. A guard that blocked ordinary work would be
// removed within the day, and then it would guard nothing.
func TestLetsACleanPushThrough(t *testing.T) {
	for _, args := range [][]string{
		{"origin", "https://github.com/go-xrkit/desk.git"},
		{"origin", "git@github.com:go-xrkit/desk.git"},
		{"origin", "/some/local/path.git"},
		{"origin"},
		{},
	} {
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb); code != 0 {
			t.Errorf("%v: exit %d, want 0 — stderr: %s", args, code, errb.String())
		}
	}
}

// TestRefusesAWriteToTheDefaultBranch is the second thing this hook is for. It
// was asked for after a fix went straight onto main by habit -- green, tested,
// and read by nobody.
func TestRefusesAWriteToTheDefaultBranch(t *testing.T) {
	var out, errb bytes.Buffer
	refs := "refs/heads/main aaaa refs/heads/main bbbb\n"
	if code := run([]string{"origin", "https://github.com/x/y.git"},
		strings.NewReader(refs), &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	msg := errb.String()
	// The refusal has to say what to do instead, or it is an obstacle rather
	// than a guard.
	for _, want := range []string{"pull requests land", "git switch -c", "gh pr create"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// TestATagStillGoesUp: the guard must not touch releases. A guard that made
// tagging harder would be turned off, and then it would guard nothing.
func TestATagStillGoesUp(t *testing.T) {
	var out, errb bytes.Buffer
	refs := "refs/tags/v1.2.3 aaaa refs/tags/v1.2.3 0000000000000000000000000000000000000000\n"
	if code := run([]string{"origin", "https://github.com/x/y.git"},
		strings.NewReader(refs), &out, &errb); code != 0 {
		t.Errorf("a tag was refused, exit %d: %s", code, errb.String())
	}
}

// TestTheRefsReachTheRepositorysOwnHook: this hook reads stdin to judge it, so
// it has to REPLAY it. A repository hook that received nothing would be
// deciding on silence.
func TestTheRefsReachTheRepositorysOwnHook(t *testing.T) {
	var out, errb bytes.Buffer
	refs := "refs/heads/a-fix aaaa refs/heads/a-fix bbbb\n"
	// No repository hook here, so this asserts what it can: an ordinary branch
	// passes, and the read of stdin did not turn that into a refusal.
	if code := run([]string{"origin", "https://github.com/x/y.git"},
		strings.NewReader(refs), &out, &errb); code != 0 {
		t.Errorf("an ordinary branch was refused, exit %d: %s", code, errb.String())
	}
}

// TestTheEscapeIsAnAct: there are writes that legitimately have nowhere else to
// go. The way through is deliberate, and it says so in the transcript.
func TestTheEscapeIsAnAct(t *testing.T) {
	t.Setenv("GITSAFE_ALLOW_DEFAULT_BRANCH", "1")
	var out, errb bytes.Buffer
	refs := "refs/heads/main aaaa refs/heads/main bbbb\n"
	if code := run([]string{"origin", "https://github.com/x/y.git"},
		strings.NewReader(refs), &out, &errb); code != 0 {
		t.Errorf("the escape did not work, exit %d: %s", code, errb.String())
	}
}
