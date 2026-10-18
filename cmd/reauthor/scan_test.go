package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cleanRepo is a checkout with one commit under the public identity only.
func cleanRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	run2(t, dir, nil, "config", "user.name", wantName)
	run2(t, dir, nil, "config", "user.email", wantEmail)
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "add", "x.txt")
	run2(t, dir, nil, "commit", "-q", "-m", "clean")
	return dir
}

func TestAScanNamesOnlyTheCheckoutsThatCarryYou(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dirty := repoWith(t) // 3 commits of "mine", 1 of somebody else's
	// Put a copy under the root so the walk finds it.
	nested := filepath.Join(root, "nested", "dirty")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, t.TempDir(), nil, "clone", "-q", "--no-local", dirty, nested)
	cleanRepo(t, filepath.Join(root, "tidy"))

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 {
		t.Errorf("a scan that FINDS something must exit non-zero, got %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "nested/dirty") {
		t.Errorf("the carrying repository is not named:\n%s", out)
	}
	if strings.Contains(out, filepath.Join(root, "tidy")) {
		t.Errorf("a clean repository was reported:\n%s", out)
	}
	if !strings.Contains(out, "2 checkouts") {
		t.Errorf("the checkout count is wrong:\n%s", out)
	}
	// The summary says REPOSITORIES because worktrees are folded in: a worktree
	// is not a separate publication.
	if !strings.Contains(out, "1 checkout(s) carry") {
		t.Errorf("the carrier count is wrong:\n%s", out)
	}
	// ⭐ The other contributor's address must NOT be counted.
	if strings.Contains(out, otherEmail) {
		t.Errorf("a third party's identity was counted:\n%s", out)
	}
}

func TestAScanOfCleanCheckoutsExitsZero(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	cleanRepo(t, filepath.Join(root, "a"))
	cleanRepo(t, filepath.Join(root, "b"))
	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 0 {
		t.Errorf("exit %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "nothing of yours is published") {
		t.Errorf("stdout = %q", out)
	}
}

// ⛔ The committer counts as much as the author: a commit written by somebody
// else and rebased by you carries YOUR address as committer, and it is published
// just as visibly.
func TestTheCommitterCountsToo(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := filepath.Join(root, "r")
	cleanRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "y.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "add", "y.txt")
	run2(t, dir, []string{
		"GIT_AUTHOR_NAME=" + otherName, "GIT_AUTHOR_EMAIL=" + otherEmail,
		"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
	}, "commit", "-q", "-m", "written by them, committed by me")

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, _ := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 || !strings.Contains(out, mineEmail) {
		t.Errorf("a committer address went unseen: exit %d\n%s", code, out)
	}
}

func TestAScanRefusesWithoutRoots(t *testing.T) {
	hermetic(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	code, _, errb := exec2(t, "-identities", ids, "-scan")
	if code != 2 || !strings.Contains(errb, "needs the roots spelled out") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
}

// ⭐ A scan needs neither a configured identity nor any write, so it must NOT
// refuse for want of user.name — otherwise the detecting half would be unusable
// exactly where it helps most.
func TestAScanWorksWithNoConfiguredIdentity(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := filepath.Join(root, "r")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	ids := identitiesFile(t, "email: "+mineEmail)
	if code, out, errb := exec2(t, "-identities", ids, "-scan", root); code != 0 {
		t.Errorf("exit %d (a configured identity must not be required)\n%s%s", code, out, errb)
	}
}

// ⛔ An empty repository is not an unreadable one: git refuses `log` with a
// non-zero status, and taking that for a read failure would make every scan
// report itself "incomplete".
func TestAnEmptyRepositoryIsNotUnreadable(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := filepath.Join(root, "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 0 {
		t.Errorf("exit %d: an empty repository was taken for an unreadable one\n%s%s", code, out, errb)
	}
	if strings.Contains(errb, "did not cover everything") {
		t.Errorf("stderr announces an incomplete scan: %q", errb)
	}
}

// ⛔ The first reading of the fleet scan was "350 repositories carry an
// identity" — while 215 were on an institution's GitLab, where that address
// belongs, and 71 had no remote at all. The total alone is the wrong number to
// act on, and the breakdown was done by hand, outside the tool. So it is in the
// tool now.
func TestTheSummaryGroupsByWhereOriginPoints(t *testing.T) {
	hermetic(t)
	root := t.TempDir()

	// Three carriers: one published on a public host, one on an internal forge,
	// one with no remote at all.
	mk := func(name, remote string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "init", "-q", "-b", "main", ".")
		run2(t, dir, nil, "config", "user.name", wantName)
		run2(t, dir, nil, "config", "user.email", wantEmail)
		if remote != "" {
			run2(t, dir, nil, "config", "remote.origin.url", remote)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "add", "f.txt")
		run2(t, dir, []string{
			"GIT_AUTHOR_NAME=" + mineName, "GIT_AUTHOR_EMAIL=" + mineEmail,
			"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
		}, "commit", "-q", "-m", "mine")
	}
	mk("public", "https://github.invalid/o/r.git")
	// ⭐ The scp form has no scheme: credurl is what knows how to read it, and
	// that is the reason not to write a second URL parser here.
	mk("internal", "git@gitlab.internal.invalid:group/proj.git")
	mk("nowhere", "")

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	for _, want := range []string{
		"by UPSTREAM, grouped by host",
		"github.invalid",
		"gitlab.internal.invalid",
		"(no remote — published nowhere)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary does not say %q:\n%s", want, out)
		}
	}
	// Each group holds exactly one repository and one commit.
	if n := strings.Count(out, "commit(s) in    1 repo(s)"); n != 3 {
		t.Errorf("3 single-repository groups expected, %d found:\n%s", n, out)
	}
	// ⛔ And the host must appear on EVERY repository's line: a table counting 88
	// repositories on a public host without naming them is not actionable, and
	// the reader then regroups the output by PATH to find them — the very
	// mistake this grouping exists to avoid.
	// ⛔ The UPSTREAM on the line, not only its host: the summary counts
	// upstreams, and naming only the host leaves the reader hunting for which.
	for _, want := range []string{
		filepath.Join(root, "public") + "\n    github.invalid/o/r",
		filepath.Join(root, "nowhere") + "\n    (no remote",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the repository's line does not name its host (%q):\n%s", want, out)
		}
	}
}

// A local path as a remote IS a remote, and it is not "no remote".
func TestALocalPathRemoteIsNotNoRemote(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := filepath.Join(root, "cloned")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	run2(t, dir, nil, "config", "user.name", wantName)
	run2(t, dir, nil, "config", "user.email", wantEmail)
	run2(t, dir, nil, "config", "remote.origin.url", "/some/local/path.git")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "add", "f.txt")
	run2(t, dir, []string{
		"GIT_AUTHOR_NAME=" + mineName, "GIT_AUTHOR_EMAIL=" + mineEmail,
		"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
	}, "commit", "-q", "-m", "mine")

	ids := identitiesFile(t, "email: "+mineEmail)
	_, out, _ := exec2(t, "-identities", ids, "-scan", root)
	if !strings.Contains(out, "(a local path)") {
		t.Errorf("a remote that is a local path is not named as one:\n%s", out)
	}
	if strings.Contains(out, "no remote") {
		t.Errorf("a local path was taken for an absent remote:\n%s", out)
	}
}

// ⛔⛔ The defect that moved the number a third time. The first grouped scan
// announced "89 carrying GitHub repositories, 13323 commits" — of which some
// eighty were agent worktrees of a SINGLE fork, each counting the same history.
// A worktree is not a separate publication.
func TestAWorktreeIsFoldedIntoItsRepository(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	main := filepath.Join(root, "repo")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, main, nil, "init", "-q", "-b", "main", ".")
	run2(t, main, nil, "config", "user.name", wantName)
	run2(t, main, nil, "config", "user.email", wantEmail)
	run2(t, main, nil, "config", "remote.origin.url", "https://github.invalid/o/r.git")
	if err := os.WriteFile(filepath.Join(main, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run2(t, main, nil, "add", "f.txt")
	run2(t, main, []string{
		"GIT_AUTHOR_NAME=" + mineName, "GIT_AUTHOR_EMAIL=" + mineEmail,
		"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
	}, "commit", "-q", "-m", "mine")
	// Two worktrees, under the repository itself, as agent worktrees are.
	run2(t, main, nil, "worktree", "add", "-q", "-b", "wt1", filepath.Join(main, "wt", "one"))
	run2(t, main, nil, "worktree", "add", "-q", "-b", "wt2", filepath.Join(main, "wt", "two"))

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "1 checkout(s) carry") || !strings.Contains(out, "1 distinct upstream(s)") {
		t.Errorf("the repository and its 2 worktrees are not folded into ONE:\n%s", out)
	}
	if !strings.Contains(out, "2 of the carriers were worktrees") {
		t.Errorf("the folding is not stated:\n%s", out)
	}
	// ⭐ And it is the repository ITSELF that is named, not one of its
	// worktrees: that is the directory somebody acts on.
	if !strings.Contains(out, main+"\n") {
		t.Errorf("the main repository is not the one named:\n%s", out)
	}
	// The per-host table counts ONE repository, not three.
	if !strings.Contains(out, "in    1 repo(s)   github.invalid") {
		t.Errorf("the per-host table is counting the worktrees:\n%s", out)
	}
}

// ⛔⛔ The fourth time the number moved, and the same cause: the unit being
// counted. Three clones of one project were counted three times, and twelve
// sandbox clones of a fork twelve times. What gets fixed is the UPSTREAM, once.
//
// ⭐ And both spellings must answer alike: `git@host:o/r.git` and
// `https://host/o/r` are the same repository.
func TestSeveralClonesOfOneUpstreamCountOnce(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	mk := func(name, remote string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "init", "-q", "-b", "main", ".")
		run2(t, dir, nil, "config", "user.name", wantName)
		run2(t, dir, nil, "config", "user.email", wantEmail)
		run2(t, dir, nil, "config", "remote.origin.url", remote)
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "add", "f.txt")
		run2(t, dir, []string{
			"GIT_AUTHOR_NAME=" + mineName, "GIT_AUTHOR_EMAIL=" + mineEmail,
			"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
		}, "commit", "-q", "-m", "mine")
	}
	mk("clone-ssh", "git@github.invalid:o/r.git")
	mk("clone-https", "https://github.invalid/o/r")
	mk("elsewhere", "https://github.invalid/o/other.git")

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "3 checkout(s) carry") {
		t.Errorf("all 3 checkouts must be listed, none hidden:\n%s", out)
	}
	if !strings.Contains(out, "2 distinct upstream(s) to fix") {
		t.Errorf("two spellings of one upstream were not folded together:\n%s", out)
	}
	if !strings.Contains(out, "1 of the carriers are further clones") {
		t.Errorf("the folded clone is not stated:\n%s", out)
	}
	if !strings.Contains(out, "in    2 repo(s)   github.invalid") {
		t.Errorf("the per-host table must count 2 upstreams:\n%s", out)
	}
}
