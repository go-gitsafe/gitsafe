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

// TestARefusalRestingOnOriginHeadSaysSo is the message half of a false refusal
// a peer session measured on 2026-10-31: `gitpush --force-with-lease origin
// flag-to-cobra` was refused as "that is where pull requests land" while GitHub
// said the default branch was main. The clone had been made FROM ANOTHER CLONE,
// so refs/remotes/origin/HEAD had inherited the source clone's working branch,
// and setting origin's URL afterwards did not update it.
//
// Clone-of-a-clone is the isolation pattern this machine recommends for
// parallel sessions, so the guard will meet this again. The refusal was right
// to fire -- it fails closed -- but it named a branch without saying where the
// name came from, and the reader had no way to guess a stale local pointer.
func TestARefusalRestingOnOriginHeadSaysSo(t *testing.T) {
	was := remoteDefault
	t.Cleanup(func() { remoteDefault = was })
	remoteDefault = func(string) string { return "flag-to-cobra" }

	var out, errb bytes.Buffer
	refs := "refs/heads/flag-to-cobra aaaa refs/heads/flag-to-cobra bbbb\n"
	if code := run([]string{"origin", "https://github.com/x/y.git"},
		strings.NewReader(refs), &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	msg := errb.String()
	for _, want := range []string{
		"is not main or master",
		"refs/remotes/origin/HEAD",
		"local cache written at clone time",
		"git remote set-head origin --auto",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, msg)
		}
	}
}

// TestTheDangerousDirectionIsUnchanged is the control, and it is the half that
// matters: the explanation above must not have loosened the guard by one
// character. With origin/HEAD naming SOME OTHER branch, a push to main is still
// refused -- main is protected by NAME as well as by the pointer -- and the
// origin/HEAD paragraph must NOT appear, because it would be false there.
//
// ⛔ Without this row, a change that made the pointer the ONLY source of
// protection would pass the test above and quietly stop refusing main.
func TestTheDangerousDirectionIsUnchanged(t *testing.T) {
	was := remoteDefault
	t.Cleanup(func() { remoteDefault = was })
	remoteDefault = func(string) string { return "some-other-branch" }

	var out, errb bytes.Buffer
	refs := "refs/heads/main aaaa refs/heads/main bbbb\n"
	if code := run([]string{"origin", "https://github.com/x/y.git"},
		strings.NewReader(refs), &out, &errb); code != 1 {
		t.Fatalf("a push to main was NOT refused while origin/HEAD named another branch: exit %d", code)
	}
	if msg := errb.String(); strings.Contains(msg, "refs/remotes/origin/HEAD") {
		t.Errorf("main is protected by name, so the origin/HEAD explanation is false here:\n%s", msg)
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

// go-quake1/engine#79: a build binary committed by `git add -A`, removed by
// the next commit. The tip is clean; the push still sends it, so it is refused.
func TestRefusesALargeFileEvenWhenALaterCommitDeletesIt(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "README.md")
	gitIn(t, dir, "commit", "-qm", "first")
	base := gitIn(t, dir, "rev-parse", "HEAD")

	big := make([]byte, 2<<20)
	for i := range big {
		big[i] = byte(i * 7) // not all zeros, so nothing compresses it away
	}
	if err := os.WriteFile(filepath.Join(dir, "quake-wasmbox"), big, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "E2 (with the build output by mistake)")
	gitIn(t, dir, "rm", "-q", "quake-wasmbox")
	gitIn(t, dir, "commit", "-qm", "remove the binary")
	tip := gitIn(t, dir, "rev-parse", "HEAD")
	if out := gitIn(t, dir, "ls-tree", "-r", "--name-only", tip); strings.Contains(out, "quake-wasmbox") {
		t.Fatalf("the tip still holds it; the test does not test what it says:\n%s", out)
	}

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
	if code := judgeBigBlobs([]string{"origin"}, []byte(refs), &errb); code != 1 {
		t.Fatalf("exit %d, want 1 — the deleted binary was not refused:\n%s", code, errb.String())
	}
	for _, want := range []string{"quake-wasmbox", "2.0 MiB", "GITSAFE_ALLOW_BIGBLOB", "refs/pull"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, errb.String())
		}
	}
	// A creation (no remote value) is judged too.
	created := "refs/heads/work " + tip + " refs/heads/work 0000000000000000000000000000000000000000\n"
	var errc bytes.Buffer
	if code := judgeBigBlobs([]string{"origin"}, []byte(created), &errc); code != 1 {
		t.Errorf("a new branch carrying it: exit %d, want 1:\n%s", code, errc.String())
	}

	// From the binary's removal onward, nothing large is sent: let it through.
	removal := gitIn(t, dir, "rev-parse", "HEAD~1")
	clean := "refs/heads/work " + tip + " refs/heads/work " + removal + "\n"
	var errd bytes.Buffer
	if code := judgeBigBlobs([]string{"origin"}, []byte(clean), &errd); code != 0 {
		t.Errorf("a push sending only the removal: exit %d:\n%s", code, errd.String())
	}

	t.Setenv("GITSAFE_ALLOW_BIGBLOB", "1")
	var erre bytes.Buffer
	if code := judgeBigBlobs([]string{"origin"}, []byte(refs), &erre); code != 0 {
		t.Errorf("GITSAFE_ALLOW_BIGBLOB did not let it through: %s", erre.String())
	}
}

// What git cannot read, and a deletion, are not refused by this judge.
func TestBigBlobGuardIgnoresWhatItShould(t *testing.T) {
	const zero = "0000000000000000000000000000000000000000"
	for name, refs := range map[string]string{
		"a deletion":        "(delete) " + zero + " refs/heads/gone bbbb\n",
		"an unreadable sha": "refs/heads/x notasha refs/heads/x alsonot\n",
		"nothing at all":    "",
	} {
		var errb bytes.Buffer
		if code := judgeBigBlobs([]string{"origin"}, []byte(refs), &errb); code != 0 {
			t.Errorf("%s: exit %d, want 0:\n%s", name, code, errb.String())
		}
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
