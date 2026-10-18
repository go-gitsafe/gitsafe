package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ The identities in these tests are `.invalid` addresses, the TLD RFC 2606
// reserves for exactly this. A test carrying a real address would do the very
// thing this tool exists to undo.
const (
	mineEmail  = "someone@old.invalid"
	mineName   = "Some One"
	otherEmail = "yannick@elsewhere.invalid"
	otherName  = "Yannick Fitamant"
	wantName   = "publicname"
	wantEmail  = "publicname@users.noreply.github.invalid"
)

func hermetic(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

func run2(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoWith builds a repository: a commit of yours, one of somebody else's, a
// merge, a lightweight tag and an annotated tag whose message has a BODY.
func repoWith(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	run2(t, dir, nil, "config", "user.name", wantName)
	run2(t, dir, nil, "config", "user.email", wantEmail)

	as := func(n, e, when string) []string {
		return []string{
			"GIT_AUTHOR_NAME=" + n, "GIT_AUTHOR_EMAIL=" + e, "GIT_AUTHOR_DATE=" + when,
			"GIT_COMMITTER_NAME=" + n, "GIT_COMMITTER_EMAIL=" + e, "GIT_COMMITTER_DATE=" + when,
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "add", name)
	}

	write("a.txt", "a\n")
	run2(t, dir, as(mineName, mineEmail, "2025-01-02T03:04:05+0200"), "commit", "-q", "-m", "mine: root")

	run2(t, dir, nil, "checkout", "-q", "-b", "side")
	write("b.txt", "b\n")
	run2(t, dir, as(otherName, otherEmail, "2025-02-03T04:05:06+0100"), "commit", "-q", "-m", "theirs: on a branch")

	run2(t, dir, nil, "checkout", "-q", "main")
	write("c.txt", "c\n")
	run2(t, dir, as(mineName, mineEmail, "2025-03-04T05:06:07+0300"), "commit", "-q", "-m", "mine: second")
	run2(t, dir, as(mineName, mineEmail, "2025-04-05T06:07:08+0000"),
		"merge", "-q", "--no-ff", "side", "-m", "mine: the merge")

	run2(t, dir, nil, "tag", "v0.0.1-light")
	run2(t, dir, as(mineName, mineEmail, "2025-05-06T07:08:09+0200"),
		"tag", "-a", "v0.1.0", "-m", "v0.1.0\n\nA body that must survive.\nTwo lines of it.")
	return dir
}

func identitiesFile(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "identities")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func exec2(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestYoursIsRewrittenAndNobodyElsesIs(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)

	before := run2(t, dir, nil, "log", "--branches", "--tags", "--format=%an <%ae>%x09%aI%x09%cI%x09%s")
	code, out, errb := exec2(t, "-identities", ids, dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	after := run2(t, dir, nil, "log", "--branches", "--tags", "--format=%an <%ae>%x09%aI%x09%cI%x09%s")

	// ⭐ The DATES must be identical line for line, and only the identities may
	// have moved.
	bl, al := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(bl) != len(al) {
		t.Fatalf("%d commits before, %d after", len(bl), len(al))
	}
	for i := range bl {
		bf, af := strings.Split(bl[i], "\t"), strings.Split(al[i], "\t")
		if bf[1] != af[1] || bf[2] != af[2] {
			t.Errorf("dates changed on %q: %v -> %v", bf[3], bf[1:3], af[1:3])
		}
		if bf[3] != af[3] {
			t.Errorf("message changed: %q -> %q", bf[3], af[3])
		}
		isMine := strings.Contains(bf[0], mineEmail)
		wantWho := bf[0]
		if isMine {
			wantWho = wantName + " <" + wantEmail + ">"
		}
		if af[0] != wantWho {
			t.Errorf("on %q: author = %q, want %q", bf[3], af[0], wantWho)
		}
	}

	// The other contributor must be untouched, name AND address.
	if !strings.Contains(after, otherName+" <"+otherEmail+">") {
		t.Errorf("a third party's commit was touched:\n%s", after)
	}
	if strings.Contains(after, mineEmail) {
		t.Errorf("the old address survives:\n%s", after)
	}
}

// ⛔ The defect the shell script carried: `git tag -a -m "$subject"` keeps only
// the first line, so every annotated tag with a body lost it.
func TestAnAnnotatedTagKeepsItsWholeMessageAndMovesWithIt(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	wasHead := run2(t, dir, nil, "rev-parse", "main")

	if code, out, errb := exec2(t, "-identities", ids, dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}

	body := run2(t, dir, nil, "for-each-ref", "--format=%(contents)", "refs/tags/v0.1.0")
	for _, want := range []string{"A body that must survive.", "Two lines of it."} {
		if !strings.Contains(body, want) {
			t.Errorf("the annotated tag's message lost %q; it says:\n%s", want, body)
		}
	}
	// ⛔ And it must point at the REWRITTEN commit, not the old one.
	if onto := run2(t, dir, nil, "rev-parse", "v0.1.0^{commit}"); onto == wasHead {
		t.Error("the annotated tag still points at the old commit")
	}
	if tagger := run2(t, dir, nil, "for-each-ref", "--format=%(taggeremail)", "refs/tags/v0.1.0"); strings.Contains(tagger, mineEmail) {
		t.Errorf("the tagger still carries the old address: %s", tagger)
	}
	// The lightweight tag must have followed too.
	if run2(t, dir, nil, "rev-parse", "v0.0.1-light") == wasHead {
		t.Error("the lightweight tag still points at the old commit")
	}
}

func TestEveryTreeIsUnchangedAndTheMergeKeepsItsShape(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	treeBefore := run2(t, dir, nil, "rev-parse", "main^{tree}")
	parentsBefore := run2(t, dir, nil, "rev-list", "--parents", "-n1", "main")

	if code, out, errb := exec2(t, "-identities", ids, dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if got := run2(t, dir, nil, "rev-parse", "main^{tree}"); got != treeBefore {
		t.Errorf("main's tree changed: %s -> %s", treeBefore, got)
	}
	if got := len(strings.Fields(run2(t, dir, nil, "rev-list", "--parents", "-n1", "main"))); got != len(strings.Fields(parentsBefore)) {
		t.Errorf("the merge lost a parent: %d field(s) against %d", got, len(strings.Fields(parentsBefore)))
	}
}

// A name is yours whatever the address — this is the case where a stranger's
// address carries your name. And the other way round.
func TestANameMatchesWhateverTheAddressIs(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "name: "+mineName)
	if code, out, errb := exec2(t, "-identities", ids, dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	after := run2(t, dir, nil, "log", "--branches", "--tags", "--format=%an <%ae>")
	if strings.Contains(after, mineName) {
		t.Errorf("the old NAME survives:\n%s", after)
	}
	if !strings.Contains(after, otherName) {
		t.Errorf("the third party has disappeared:\n%s", after)
	}
}

func TestRefusals(t *testing.T) {
	hermetic(t)
	t.Run("without -identities", func(t *testing.T) {
		if code, _, errb := exec2(t, t.TempDir()); code != 2 || !strings.Contains(errb, "-identities is required") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
	t.Run("an empty identities file", func(t *testing.T) {
		ids := identitiesFile(t, "# nothing but comments")
		if code, _, errb := exec2(t, "-identities", ids, t.TempDir()); code != 1 ||
			!strings.Contains(errb, "names no identity") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
	t.Run("a malformed line", func(t *testing.T) {
		ids := identitiesFile(t, "someone@old.invalid")
		if code, _, errb := exec2(t, "-identities", ids, t.TempDir()); code != 1 ||
			!strings.Contains(errb, "expected") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
	t.Run("a dirty work tree", func(t *testing.T) {
		dir := repoWith(t)
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ids := identitiesFile(t, "email: "+mineEmail)
		if code, _, errb := exec2(t, "-identities", ids, dir); code != 1 ||
			!strings.Contains(errb, "uncommitted changes to tracked files") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
	t.Run("an UNTRACKED file is not enough to refuse", func(t *testing.T) {
		dir := repoWith(t)
		if err := os.WriteFile(filepath.Join(dir, "build.log"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ids := identitiesFile(t, "email: "+mineEmail)
		if code, out, errb := exec2(t, "-identities", ids, dir); code != 0 {
			t.Errorf("an untracked file caused a refusal: exit %d\n%s%s", code, out, errb)
		}
	})
	t.Run("no branch and no tag", func(t *testing.T) {
		dir := t.TempDir()
		run2(t, dir, nil, "init", "-q", "-b", "main", ".")
		// ⛔ An identity has to be configured, or the refusal comes from its
		// absence and this test would assert the wrong refusal.
		run2(t, dir, nil, "config", "user.name", wantName)
		run2(t, dir, nil, "config", "user.email", wantEmail)
		ids := identitiesFile(t, "email: "+mineEmail)
		if code, _, errb := exec2(t, "-identities", ids, dir); code != 1 ||
			!strings.Contains(errb, "nothing would be published") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
}

// ⛔ Killing the shell that started a rewrite does not kill the rewrite: in
// August 2026 two of them were running on the same repository, both on their way
// to update-ref and a reset --hard on the same refs.
func TestASecondRewriteIsRefusedWhileOneIsRunning(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	gitdir := run2(t, dir, nil, "rev-parse", "--absolute-git-dir")
	if err := os.Mkdir(filepath.Join(gitdir, "reauthor.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	ids := identitiesFile(t, "email: "+mineEmail)
	code, _, errb := exec2(t, "-identities", ids, dir)
	if code != 1 || !strings.Contains(errb, "another rewrite is running") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
	if !strings.Contains(errb, "rmdir") {
		t.Errorf("the refusal does not say how to clear an orphaned lock: %q", errb)
	}
}

func TestADryRunWritesNothing(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	before := run2(t, dir, nil, "rev-parse", "main")
	code, out, errb := exec2(t, "-identities", ids, "-dry-run", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if got := run2(t, dir, nil, "rev-parse", "main"); got != before {
		t.Errorf("a dry run rewrote main: %s -> %s", before, got)
	}
	for _, want := range []string{"4 commits: 3 yours, 1 left alone", "nothing was written"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not say %q:\n%s", want, out)
		}
	}
}

func TestToOverridesTheConfiguredIdentity(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	if code, out, errb := exec2(t, "-identities", ids, "-to", "Someone Else <else@example.invalid>", dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if after := run2(t, dir, nil, "log", "--branches", "--format=%an <%ae>"); !strings.Contains(after, "else@example.invalid") {
		t.Errorf("-to was not used:\n%s", after)
	}
	t.Run("a refused form", func(t *testing.T) {
		if code, _, errb := exec2(t, "-identities", ids, "-to", "not an identity", dir); code != 1 ||
			!strings.Contains(errb, `-to must be`) {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
}
