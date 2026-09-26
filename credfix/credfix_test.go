package credfix

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeToken is SHAPED like a GitHub classic personal access token and is not
// one: the prefix is assembled and the body is visibly a repeated pattern, so
// that nothing scanning this repository has to treat it as live. Only the shape
// matters to the code under test.
var fakeToken = "gh" + "p_" + strings.Repeat("Ab3", 12)

// hermetic gives the test its own git: no user, no global configuration, no
// hooks. Without it a test reads whatever the machine it runs on happens to
// have, and a green run says nothing about anywhere else.
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

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoWith makes a real repository whose origin carries a credential in both
// its fetch and its push URL — the shape 117 checkouts on this machine were in.
func repoWith(t *testing.T, dir, fetch, push string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "init", "-q", ".")
	run(t, dir, "config", "--local", "user.name", "test")
	run(t, dir, "config", "--local", "user.email", "test@example.invalid")
	run(t, dir, "config", "--local", "remote.origin.url", fetch)
	if push != "" {
		run(t, dir, "config", "--local", "remote.origin.pushurl", push)
	}
	return dir
}

// TestRepairOnARealRepository is the whole of clean, on a real repository: it
// finds both URLs, a dry run leaves them alone, a write strips them, the values
// are read back out of git rather than assumed, and a second run is a no-op.
func TestRepairOnARealRepository(t *testing.T) {
	hermetic(t)
	dir := repoWith(t, filepath.Join(t.TempDir(), "r"),
		"https://user:"+fakeToken+"@github.com/o/r.git",
		"https://"+fakeToken+"@github.com/o/r.git")

	r := Inspect(dir)
	if !r.Readable() {
		t.Fatalf("not readable: %v", r.Err)
	}
	if r.Before() != 2 {
		t.Fatalf("found %d leaks, want 2 (fetch and push): %+v", r.Before(), r.Leaks)
	}
	for _, e := range r.Leaks {
		if !e.Rewritable() {
			t.Errorf("%s should be rewritable", e.Key)
		}
		if e.Clean != "https://github.com/o/r.git" {
			t.Errorf("%s: clean form = %q", e.Key, e.Clean)
		}
	}

	// A dry run must leave the repository exactly as it was. Reading the value
	// back is the only way to know: a report of what "would" happen is not
	// evidence that nothing did.
	dry := Repair(dir, false)
	if dry.After() != 2 {
		t.Errorf("a dry run reported %d URLs still dirty, want 2", dry.After())
	}
	if got := run(t, dir, "config", "--local", "--get", "remote.origin.url"); !strings.Contains(got, fakeToken) {
		t.Errorf("a dry run changed the repository")
	}

	w := Repair(dir, true)
	if w.Before() != 2 || w.After() != 0 {
		t.Fatalf("write: before=%d after=%d, want 2 and 0", w.Before(), w.After())
	}
	for _, key := range []string{"remote.origin.url", "remote.origin.pushurl"} {
		if got := run(t, dir, "config", "--local", "--get", key); got != "https://github.com/o/r.git" {
			t.Errorf("%s = %q after the repair", key, got)
		}
	}

	// Idempotent: a repair sweep over a machine that is already half repaired
	// must not need to know which half.
	again := Repair(dir, true)
	if again.Before() != 0 {
		t.Errorf("a second run found %d leaks, want 0", again.Before())
	}
}

// TestAnSshRemoteIsLeftAlone is the other half, and the half that a detector
// gets wrong more expensively: stripping `git` out of `ssh://git@github.com/…`
// would break every ssh remote it touched. 55 URLs were flagged that way here.
func TestAnSshRemoteIsLeftAlone(t *testing.T) {
	hermetic(t)
	dir := repoWith(t, filepath.Join(t.TempDir(), "r"), "ssh://git@github.com/o/r.git", "")
	run(t, dir, "config", "--local", "remote.up.url", "git@plmlab.math.cnrs.fr:team/repo")

	r := Repair(dir, true)
	if r.Before() != 0 {
		t.Fatalf("an ssh remote read as a leak: %+v", r.Leaks)
	}
	if got := run(t, dir, "config", "--local", "--get", "remote.origin.url"); got != "ssh://git@github.com/o/r.git" {
		t.Errorf("the ssh URL was rewritten to %q", got)
	}
	if got := run(t, dir, "config", "--local", "--get", "remote.up.url"); got != "git@plmlab.math.cnrs.fr:team/repo" {
		t.Errorf("the scp URL was rewritten to %q", got)
	}
}

// TestACredentialInTheKeyIsReportedWithoutBeingRepaired: `url.<base>.insteadOf`
// is a legal rewrite rule and the credential can be in the KEY. Where it should
// point instead is a decision, so it is named rather than guessed at — and the
// key this prints must have the credential taken out of it.
func TestACredentialInTheKeyIsReportedWithoutBeingRepaired(t *testing.T) {
	hermetic(t)
	dir := repoWith(t, filepath.Join(t.TempDir(), "r"), "https://github.com/o/r.git", "")
	run(t, dir, "config", "--local", "url.https://"+fakeToken+"@github.com/.insteadOf", "gh:")

	r := Repair(dir, true)
	if r.Before() != 1 {
		t.Fatalf("found %d, want 1: %+v", r.Before(), r.Leaks)
	}
	e := r.Leaks[0]
	if !e.InKey {
		t.Error("the credential was in the key and InKey is false")
	}
	if e.Rewritable() || e.Repaired {
		t.Error("a credential in a key must not be rewritten automatically")
	}
	if strings.Contains(e.Key, fakeToken) {
		t.Error("the reported key still holds the credential")
	}
}

// TestMultiValuedUrlIsLeftForAPerson. --replace-all would collapse two URLs into
// one, and this cannot know which one a person meant to keep. Naming it beats
// silently discarding a remote.
func TestMultiValuedUrlIsLeftForAPerson(t *testing.T) {
	hermetic(t)
	dir := repoWith(t, filepath.Join(t.TempDir(), "r"), "https://"+fakeToken+"@github.com/o/r.git", "")
	run(t, dir, "config", "--local", "--add", "remote.origin.url", "https://"+fakeToken+"@github.com/o/mirror.git")

	r := Repair(dir, true)
	if r.Before() != 2 {
		t.Fatalf("found %d, want 2", r.Before())
	}
	for _, e := range r.Leaks {
		if e.Repaired {
			t.Error("a multi-valued remote URL was rewritten")
		}
	}
	if n := len(strings.Split(run(t, dir, "config", "--local", "--get-all", "remote.origin.url"), "\n")); n != 2 {
		t.Errorf("the repository now has %d URLs, want 2 untouched", n)
	}
}

// TestARewriteThatDoesNotStickIsNotReportedAsRepaired.
//
// This is the rule the incident is about, as a mechanism rather than as a
// resolution: the write is made to appear to succeed while the value stays
// dirty, and Repair must still say the URL is dirty. Without the read-back this
// test passes while the machine is unrepaired, which is exactly the shape of
// failure that let 117 checkouts sit for three months.
func TestARewriteThatDoesNotStickIsNotReportedAsRepaired(t *testing.T) {
	dirty := "https://" + fakeToken + "@github.com/o/r.git"
	restore := git
	defer func() { git = restore }()
	git = func(dir string, args ...string) ([]byte, string, error) {
		switch {
		case args[0] == "config" && has(args, "--list"):
			return []byte("file:.git/config\x00remote.origin.url\n" + dirty + "\x00"), "", nil
		case args[0] == "config" && has(args, "--get-all"):
			return []byte(dirty + "\n"), "", nil
		case args[0] == "config" && has(args, "--replace-all"):
			return nil, "", nil // says it worked
		case args[0] == "config" && has(args, "--get"):
			return []byte(dirty + "\n"), "", nil // and it did not
		}
		return nil, "", errors.New("unexpected: " + strings.Join(args, " "))
	}
	r := Repair("anywhere", true)
	if r.Before() != 1 {
		t.Fatalf("found %d, want 1", r.Before())
	}
	if r.Leaks[0].Repaired || r.After() != 1 {
		t.Error("a rewrite that did not stick was reported as repaired")
	}
}

// TestAConfigurationThatReadsEmptyIsUnreadableNotClean.
//
// Every repository has core.repositoryformatversion, so an empty answer is
// never a legitimate one — and on this machine `git` can print a licence
// refusal and still exit 0. A guard that read that as "no remotes, nothing to
// see" would report a clean bill of health it never established.
func TestAConfigurationThatReadsEmptyIsUnreadableNotClean(t *testing.T) {
	restore := git
	defer func() { git = restore }()
	const said = "xcrun: error: invalid active developer path"
	git = func(dir string, args ...string) ([]byte, string, error) { return nil, said, nil }

	r := Inspect("anywhere")
	if r.Readable() {
		t.Fatal("an empty configuration was reported as readable")
	}
	if !strings.Contains(r.Err.Error(), said) {
		// What git printed while exiting 0 is usually the whole explanation,
		// and every caller that checks only the status throws it away.
		t.Errorf("git's own words were lost: %v", r.Err)
	}
	// And a repair over it must not claim to have done anything.
	if w := Repair("anywhere", true); w.Readable() || w.Before() != 0 {
		t.Error("Repair acted on a repository it could not read")
	}
}

func TestGitFailureIsReported(t *testing.T) {
	restore := git
	defer func() { git = restore }()
	git = func(dir string, args ...string) ([]byte, string, error) {
		return nil, "not a git repository", errors.New("exit status 128")
	}
	if r := Inspect("anywhere"); r.Readable() || r.Err == nil {
		t.Error("a failing git was not reported")
	}
}

// TestWalkCountsWhatItWalked, and counts a repository with linked worktrees
// ONCE.
//
// The counting is the point: a sweep here once reported "4 files" for 4413
// because the command it used had no such flag, and it read as a success. And
// the deduplication is measured rather than assumed — git prints the config
// path relative to its own working directory, so an unresolved one makes every
// repository on the machine look like the same repository.
func TestWalkCountsWhatItWalked(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	repoWith(t, filepath.Join(root, "one"), "https://"+fakeToken+"@github.com/o/one.git", "")
	repoWith(t, filepath.Join(root, "nested", "deeper", "two"), "ssh://git@github.com/o/two.git", "")
	main := repoWith(t, filepath.Join(root, "three"), "https://github.com/o/three.git", "")
	// A worktree shares its repository's configuration, so it must not be
	// counted or reported twice.
	run(t, main, "commit", "-q", "--allow-empty", "-m", "first")
	run(t, main, "worktree", "add", "-q", filepath.Join(root, "three-wt"), "-b", "side")

	var got []Report
	st := Walk([]string{root}, Inspect, func(r Report) { got = append(got, r) })

	if st.Repos != 4 {
		t.Errorf("Repos = %d, want 4 (three repositories and a worktree)", st.Repos)
	}
	if st.Unique != 3 {
		t.Errorf("Unique = %d, want 3 — the worktree shares a configuration", st.Unique)
	}
	if len(got) != 3 {
		t.Errorf("reported %d repositories, want 3", len(got))
	}
	if st.Unreadable != 0 {
		t.Errorf("Unreadable = %d", st.Unreadable)
	}
	if st.Dirs < 5 {
		t.Errorf("Dirs = %d, want at least the tree it walked", st.Dirs)
	}
	if !st.Complete() {
		t.Error("a walk that read everything reported itself incomplete")
	}
	leaks := 0
	for _, r := range got {
		leaks += r.Before()
	}
	if leaks != 1 {
		t.Errorf("found %d leaks over the tree, want exactly the one that is there", leaks)
	}
}

// TestAWalkThatFoundNothingIsNotAPass: no repository means no evidence.
func TestAWalkThatFoundNothingIsNotAPass(t *testing.T) {
	st := Walk([]string{t.TempDir()}, Inspect, func(Report) { t.Error("nothing should have been reported") })
	if st.Repos != 0 {
		t.Fatalf("Repos = %d", st.Repos)
	}
	if st.Complete() {
		t.Error("a walk that found no checkout called itself complete")
	}
}

// TestWalkCountsRefusedDirectories: a home directory always has some, and they
// are not a reason to fail — but they are a reason not to claim the walk was
// complete, so they are counted and printed.
func TestWalkCountsRefusedDirectories(t *testing.T) {
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skip("cannot make a directory unreadable here")
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	st := Walk([]string{root}, Inspect, func(Report) {})
	if st.DenyErrors == 0 {
		t.Error("a directory that refused listing was not counted")
	}
}

// TestUnreadableRepositoriesAreReportedNotSkipped.
func TestUnreadableRepositoriesAreReportedNotSkipped(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	// A .git directory with nothing in it: git will not read it as a
	// repository, and a scan must say so rather than pass over it.
	if err := os.MkdirAll(filepath.Join(root, "broken", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var got []Report
	st := Walk([]string{root}, Inspect, func(r Report) { got = append(got, r) })
	if st.Repos != 1 || st.Unreadable != 1 {
		t.Errorf("Repos=%d Unreadable=%d, want 1 and 1", st.Repos, st.Unreadable)
	}
	if len(got) != 1 || got[0].Readable() {
		t.Error("an unreadable repository was not reported")
	}
	if st.Complete() {
		t.Error("a walk with an unreadable repository called itself complete")
	}
}

func TestRoots(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := Roots(nil); len(got) != 1 || got[0] != home {
		t.Errorf("Roots(nil) = %v, want the home directory", got)
	}
	got := Roots([]string{".", "b"})
	if len(got) != 2 {
		t.Fatalf("Roots = %v", got)
	}
	for _, p := range got {
		if !filepath.IsAbs(p) {
			t.Errorf("%q is not absolute; a report must name a path a person can use", p)
		}
	}
}

// TestRewritable. The clean form of a credentialed URL is the same wherever the
// URL sits, so the key's NAME is not what decides — a scan of this machine found
// a token in `branch.<name>.remote`, which git allows to be a URL, in three
// checkouts whose remote URLs had all been cleaned months earlier.
func TestRewritable(t *testing.T) {
	for _, tc := range []struct {
		key, clean string
		inKey      bool
		want       bool
	}{
		{"remote.origin.url", "https://github.com/o/r", false, true},
		{"remote.origin.pushurl", "https://github.com/o/r", false, true},
		{"branch.main.remote", "https://github.com/o/r", false, true},
		{"submodule.x.url", "https://github.com/o/r", false, true},
		{"remote.origin.url", "", false, false},                // nothing to write
		{"url.https://github.com/.insteadof", "", true, false}, // the key carries it
		{"url.https://github.com/.insteadof", "https://x/y", true, false},
	} {
		got := (Entry{Key: tc.key, Clean: tc.clean, InKey: tc.inKey}).Rewritable()
		if got != tc.want {
			t.Errorf("Rewritable(%q, %q, inKey=%v) = %v, want %v", tc.key, tc.clean, tc.inKey, got, tc.want)
		}
	}
}

// TestACredentialInABranchRemoteIsRepaired is the case a real scan found and the
// first version of this package would have left behind: git allows
// `branch.<name>.remote` to be a URL, and three checkouts here were still
// carrying a token in one after every remote URL on the machine had been
// cleaned. A repair aimed at the key somebody thought of is a repair that leaves
// the others.
func TestACredentialInABranchRemoteIsRepaired(t *testing.T) {
	hermetic(t)
	dir := repoWith(t, filepath.Join(t.TempDir(), "r"), "https://github.com/o/r.git", "")
	run(t, dir, "config", "--local", "branch.main.remote", "https://x-access-token:"+fakeToken+"@github.com/o/r.git")

	r := Repair(dir, true)
	if r.Before() != 1 || r.After() != 0 {
		t.Fatalf("before=%d after=%d, want 1 and 0: %+v", r.Before(), r.After(), r.Leaks)
	}
	if got := run(t, dir, "config", "--local", "--get", "branch.main.remote"); got != "https://github.com/o/r.git" {
		t.Errorf("branch.main.remote = %q", got)
	}
}

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
