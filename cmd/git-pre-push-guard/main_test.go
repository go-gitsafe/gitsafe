package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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

// gitIn runs git in dir and fails the test if it will not.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// A repository inside a repository, committed by `git add -A` — which is exactly
// how nineteen agent worktrees were pushed to a public repository in one stroke.
// The guard reads the real trees git wrote, so the test builds real ones.
func TestRefusesAnUndeclaredNestedRepository(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "README.md")
	gitIn(t, dir, "commit", "-qm", "first")
	base := gitIn(t, dir, "rev-parse", "HEAD")

	nested := filepath.Join(dir, ".claude", "worktrees", "session-a")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, nested, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(nested, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, nested, "add", "f.txt")
	gitIn(t, nested, "commit", "-qm", "nested")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "ordinary work")
	tip := gitIn(t, dir, "rev-parse", "HEAD")

	// The guard runs git in the CURRENT directory, as a hook does.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	refs := "refs/heads/work " + tip + " refs/heads/work " + base + "\n"
	var errb bytes.Buffer
	if code := judgeGitlinks([]string{"origin"}, []byte(refs), &errb); code != 1 {
		t.Fatalf("exit %d, want 1 — the nested repository was not refused:\n%s", code, errb.String())
	}
	msg := errb.String()
	for _, want := range []string{".claude/worktrees/session-a", "git rm -r --cached", "GITSAFE_ALLOW_GITLINK"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, msg)
		}
	}

	// The escape is deliberate and loud rather than absent.
	t.Setenv("GITSAFE_ALLOW_GITLINK", "1")
	var errb2 bytes.Buffer
	if code := judgeGitlinks([]string{"origin"}, []byte(refs), &errb2); code != 0 {
		t.Errorf("GITSAFE_ALLOW_GITLINK did not let it through: %s", errb2.String())
	}
}

// The push that changes nothing of the kind is not the guard's business, and a
// deletion has no commit to read at all.
func TestGitlinkGuardIgnoresWhatItShould(t *testing.T) {
	const zero = "0000000000000000000000000000000000000000"
	for name, refs := range map[string]string{
		"a deletion":        "(delete) " + zero + " refs/heads/gone bbbb\n",
		"an unreadable sha": "refs/heads/x notasha refs/heads/x alsonot\n",
		"nothing at all":    "",
	} {
		var errb bytes.Buffer
		if code := judgeGitlinks([]string{"origin"}, []byte(refs), &errb); code != 0 {
			t.Errorf("%s: exit %d, want 0:\n%s", name, code, errb.String())
		}
	}
}

func TestTopDir(t *testing.T) {
	for in, want := range map[string]string{
		".claude/worktrees/a": ".claude",
		"vendor/sub":          "vendor",
		"bare":                "bare",
		"/leading":            "/leading",
	} {
		if got := topDir(in); got != want {
			t.Errorf("topDir(%q) = %q, want %q", in, got, want)
		}
	}
	if got := order0(nil, nil); got != "<path>" {
		t.Errorf("order0 with nothing = %q, want a placeholder", got)
	}
}
