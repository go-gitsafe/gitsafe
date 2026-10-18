package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/credfix"
)

// fakeToken is SHAPED like a GitHub classic personal access token and is not
// one — assembled prefix, a body that is visibly a repeated pattern. A fixture
// that looked live would have to be treated as live.
var fakeToken = "gh" + "p_" + strings.Repeat("Ab3", 12)

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

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func repo(t *testing.T, dir, url string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", ".")
	gitIn(t, dir, "config", "--local", "remote.origin.url", url)
	return dir
}

func exec1(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// TestScanFindsAndExitsNonZero, and prints nothing that discloses the
// credential. Both halves are asserted: an exit status nobody can act on and a
// report that leaks are each a failure of the same tool.
func TestScanFindsAndExitsNonZero(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	repo(t, filepath.Join(root, "dirty"), "https://user:"+fakeToken+"@github.com/o/r.git")
	repo(t, filepath.Join(root, "ssh"), "ssh://git@github.com/o/r.git")

	code, out, errb := exec1(t, "scan", root)
	if code != exitFound {
		t.Errorf("exit = %d, want %d\n%s%s", code, exitFound, out, errb)
	}
	if strings.Contains(out+errb, fakeToken) {
		t.Error("the credential was printed")
	}
	// Which key, what kind of credential, a digest telling it from another,
	// where it points, what was walked, how many were found, and what to do.
	for _, want := range []string{
		"remote.origin.url",
		"GitHub classic personal access token",
		"sha256:",
		"github.com",
		"walked",
		"found 2 checkouts",
		"1 of them carry a credential",
		"would become https://github.com/o/r.git",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, filepath.Join(root, "ssh")) {
		t.Error("an ssh remote was reported; 55 of those were flagged by hand once")
	}
}

func TestScanOfACleanTreeExitsZero(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	repo(t, filepath.Join(root, "a"), "https://github.com/o/a.git")
	repo(t, filepath.Join(root, "b"), "git@plmlab.math.cnrs.fr:team/repo")

	code, out, errb := exec1(t, "scan", "--verbose", root)
	if code != exitClean {
		t.Errorf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "found 2 checkouts") {
		t.Errorf("a clean scan must still say what it read:\n%s", out)
	}
	if strings.Count(out, "ok   ") != 2 {
		t.Errorf("--verbose did not list the clean checkouts:\n%s", out)
	}
}

// TestAScanThatWalkedNothingIsNotClean. Exit 0 here would be the sweep that
// reported "4 files" for 4413 and read as a success.
func TestAScanThatWalkedNothingIsNotClean(t *testing.T) {
	code, out, errb := exec1(t, "scan", t.TempDir())
	if code != exitInconclusive {
		t.Errorf("exit = %d, want %d\n%s%s", code, exitInconclusive, out, errb)
	}
	if !strings.Contains(errb, "did not cover everything") {
		t.Errorf("silence was not explained:\n%s", errb)
	}
	if !strings.Contains(out, "found 0 checkouts") {
		t.Errorf("the zero was not printed as a zero:\n%s", out)
	}
}

// TestCleanWritesOnlyWhenTold, on a real repository, verifying by reading the
// configuration back out of git rather than by trusting the report.
func TestCleanWritesOnlyWhenTold(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := repo(t, filepath.Join(root, "dirty"), "https://"+fakeToken+"@github.com/o/r.git")
	gitIn(t, dir, "config", "--local", "remote.origin.pushurl", "https://user:"+fakeToken+"@github.com/o/r.git")

	code, out, _ := exec1(t, "clean", root)
	if code != exitFound {
		t.Errorf("a dry run over a dirty tree exited %d, want %d", code, exitFound)
	}
	if !strings.Contains(out, "dry run") {
		t.Errorf("a run that writes nothing must say so:\n%s", out)
	}
	if got := gitIn(t, dir, "config", "--local", "--get", "remote.origin.url"); !strings.Contains(got, fakeToken) {
		t.Fatal("the dry run wrote")
	}

	code, out, _ = exec1(t, "clean", root, "--write")
	if code != exitClean {
		t.Errorf("exit = %d, want 0 after a complete repair:\n%s", code, out)
	}
	if !strings.Contains(out, "stripped and read back") || !strings.Contains(out, "2 before, 0 after") {
		t.Errorf("the report does not say what changed:\n%s", out)
	}
	for _, key := range []string{"remote.origin.url", "remote.origin.pushurl"} {
		if got := gitIn(t, dir, "config", "--local", "--get", key); got != "https://github.com/o/r.git" {
			t.Errorf("%s = %q", key, got)
		}
	}

	// Again, and nothing happens.
	code, out, _ = exec1(t, "clean", root, "--write")
	if code != exitClean || !strings.Contains(out, "0 of them carry a credential") {
		t.Errorf("the second run was not a no-op: exit=%d\n%s", code, out)
	}
}

// TestCleanWriteRefusesTheWholeHomeDirectory. Defaulting to $HOME is right for a
// scan and wrong for a rewrite: this machine has 2155 checkouts under it, and
// somebody repairing one did not ask for the other 2154.
func TestCleanWriteRefusesTheWholeHomeDirectory(t *testing.T) {
	code, _, errb := exec1(t, "clean", "--write")
	if code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errb, "spelled out") {
		t.Errorf("the refusal does not say what to do instead: %s", errb)
	}
}

func TestUsageAndRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"no arguments", nil, exitUsage},
		{"unknown verb", []string{"fix"}, exitUsage},
		{"scan cannot write", []string{"scan", "--write"}, exitUsage},
		{"bad flag", []string{"scan", "--nope"}, exitUsage},
		{"help", []string{"--help"}, exitClean},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errb := exec1(t, tc.args...)
			if code != tc.want {
				t.Errorf("exit = %d, want %d\n%s%s", code, tc.want, out, errb)
			}
			// A refusal that says nothing is a refusal nobody can act on, and
			// every one of these must arrive on stderr rather than on stdout:
			// a cron job that mails its output would otherwise mail silence.
			if tc.want == exitUsage && strings.TrimSpace(errb) == "" {
				t.Errorf("a refusal with nothing on stderr (stdout: %q)", out)
			}
			if tc.want == exitClean && !strings.Contains(out, "usage:") {
				t.Errorf("--help printed no usage: %q", out)
			}
		})
	}
}

// TestFlagsAfterTheRoots: `credscan clean ~/src --write` is what a person types,
// and Go's flag package stops at the first positional. A --write silently
// ignored would report a repair that never happened.
func TestFlagsAfterTheRoots(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := repo(t, filepath.Join(root, "dirty"), "https://"+fakeToken+"@github.com/o/r.git")

	code, out, _ := exec1(t, "clean", root, "--write")
	if code != exitClean {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if got := gitIn(t, dir, "config", "--local", "--get", "remote.origin.url"); got != "https://github.com/o/r.git" {
		t.Errorf("--write after the root was ignored: %q", got)
	}
}

// TestWhatItWillNotRepairItNames. Three findings it must report and refuse to
// rewrite, each for a different reason. A tool that silently skipped them would
// leave a disclosed credential behind a green exit status.
func TestWhatItWillNotRepairItNames(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	// A credential in the KEY of a rewrite rule.
	a := repo(t, filepath.Join(root, "insteadof"), "https://github.com/o/r.git")
	gitIn(t, a, "config", "--local", "url.https://"+fakeToken+"@github.com/.insteadOf", "gh:")
	// A credential in an scp-style URL, which cannot be repaired by dropping
	// the user: `host:path` is ambiguous.
	repo(t, filepath.Join(root, "scp"), fakeToken+"@github.com:o/r.git")
	// Two values under one key: this cannot know which a person meant to keep.
	c := repo(t, filepath.Join(root, "multi"), "https://"+fakeToken+"@github.com/o/r.git")
	gitIn(t, c, "config", "--local", "--add", "remote.origin.url", "https://"+fakeToken+"@github.com/o/m.git")

	code, out, errb := exec1(t, "clean", root, "--write")
	if code != exitFound {
		t.Errorf("exit = %d, want %d\n%s%s", code, exitFound, out, errb)
	}
	if strings.Contains(out+errb, fakeToken) {
		t.Error("the credential was printed")
	}
	for _, want := range []string{
		"configuration KEY",
		"scp-style URL cannot be repaired",
		"no unambiguous clean form",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not explain %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "stripped and read back"); n != 0 {
		t.Errorf("%d of these were rewritten; none should have been", n)
	}
}

// TestARewriteThatCannotBeWrittenSaysSoLoudly. The wrong outcome here is not the
// failure, it is a report that reads like a repair.
//
// Making the config file itself read-only does NOT work, and finding that out is
// worth the comment: git writes a configuration by creating config.lock beside
// it and renaming, so the file's own mode never comes into it. The DIRECTORY has
// to be unwritable.
func TestARewriteThatCannotBeWrittenSaysSoLoudly(t *testing.T) {
	hermetic(t)
	if os.Geteuid() == 0 {
		t.Skip("root writes anything")
	}
	root := t.TempDir()
	dir := repo(t, filepath.Join(root, "locked"), "https://"+fakeToken+"@github.com/o/r.git")
	gitdir := filepath.Join(dir, ".git")
	if err := os.Chmod(gitdir, 0o555); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = os.Chmod(gitdir, 0o755) })

	code, out, _ := exec1(t, "clean", root, "--write")
	if code != exitFound {
		t.Errorf("exit = %d, want %d\n%s", code, exitFound, out)
	}
	if !strings.Contains(out, "REWRITE FAILED") {
		t.Errorf("a write that did not happen was not reported as one:\n%s", out)
	}
}

// TestAnUnreadableCheckoutMakesTheRunInconclusive, when nothing was found: on
// this machine a git that cannot run still exits 0.
//
// And when something WAS found, the finding outranks it — measured on this
// machine, a full $HOME scan found 3 credentials and 21 checkouts it could not
// read, and answering only "inconclusive" would have buried the credentials.
func TestAnUnreadableCheckoutMakesTheRunInconclusive(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	repo(t, filepath.Join(root, "fine"), "https://github.com/o/r.git")
	// ⛔ An EMPTY .git directory used to stand in for this, and it is a
	// different thing: with no config there is no remote URL, so nothing to
	// leak, and git itself calls it "not a git repository". The case this
	// test is about is a configuration that EXISTS and will not open.
	broken := filepath.Join(root, "broken", ".git")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(broken, "config")
	if err := os.WriteFile(cfg, []byte("[core]\n\trepositoryformatversion = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfg, 0o644) })
	if f, err := os.Open(cfg); err == nil {
		f.Close()
		t.Skip("this user can read a 000 file (root?), so an unreadable config cannot be staged here")
	}
	code, out, errb := exec1(t, "scan", root)
	if code != exitInconclusive {
		t.Errorf("exit = %d, want %d\n%s%s", code, exitInconclusive, out, errb)
	}
	if !strings.Contains(out, "1 unreadable") {
		t.Errorf("the unreadable checkout was not counted:\n%s", out)
	}

	// Now give it something to find as well.
	repo(t, filepath.Join(root, "dirty"), "https://"+fakeToken+"@github.com/o/r.git")
	code, out, errb = exec1(t, "scan", root)
	if code != exitFound {
		t.Errorf("exit = %d, want %d — a finding outranks an incomplete walk\n%s%s", code, exitFound, out, errb)
	}
	if !strings.Contains(errb, "did not cover everything") {
		t.Errorf("the incomplete walk was no longer mentioned:\n%s", errb)
	}
}

// TestAGitDirectoryWithoutAConfigIsNotACheckout.
//
// ⛔ A scan of this machine reported 137 checkouts that "would not answer",
// and exit 3 with them. Every one had NO .git/config: build-cache trees
// holding objects/ and refs/, and worktree pointers whose target had been
// pruned. No configuration means no remote URL, so no credential — and
// counting them as unreadable buried that under a number that looked like
// risk.
func TestAGitDirectoryWithoutAConfigIsNotACheckout(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	repo(t, filepath.Join(root, "fine"), "https://github.com/o/r.git")
	// objects and refs, no config: what a brewkit cache tree looks like.
	for _, d := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(root, "cache", ".git", d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// a worktree pointer whose target is gone.
	if err := os.MkdirAll(filepath.Join(root, "orphan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "orphan", ".git"),
		[]byte("gitdir: "+filepath.Join(root, "nowhere", ".git", "worktrees", "x")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errb := exec1(t, "scan", root)
	if code != exitClean {
		t.Errorf("exit = %d, want %d — nothing here can hold a credential\n%s%s", code, exitClean, out, errb)
	}
	if !strings.Contains(out, "0 unreadable") {
		t.Errorf("a .git with no config is not unreadable:\n%s", out)
	}
	if !strings.Contains(out, "skipped 2 .git entr") {
		t.Errorf("the two were not accounted for out loud:\n%s", out)
	}
	if !strings.Contains(out, "found 1 checkouts") {
		t.Errorf("the one real checkout should be the only one counted:\n%s", out)
	}
}

// ⛔ A COUNT WITH NO REASON, two lines above a verdict about coverage. Until
// 2026-10-18 this tool said, on a real home directory:
//
//	found 2538 checkouts, 2283 distinct configurations, 0 unreadable, 243 directories refused listing
//	0 of them carry a credential: 0 URL(s) still do
//
// while the line above it explained its own number in full. A reader could not
// tell a coverage gap from a non-gap — in the output of a tool whose entire
// purpose is a claim about coverage.
func TestTheRefusedDirectoriesGetTheirOwnLineAndAReason(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	repo(t, filepath.Join(root, "fine"), "https://github.com/o/r.git")
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

	_, out, _ := exec1(t, "scan", root)
	// The count has left the "found" line.
	if strings.Contains(out, "unreadable, 1 directories refused") {
		t.Errorf("the refusal count is still buried in the counts line:\n%s", out)
	}
	for _, want := range []string{
		"skipped 1 director(ies) that refused listing",
		"ordinary permissions a person could grant",
		"--verbose names them",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal line does not say %q:\n%s", want, out)
		}
	}
	// ⭐ And it must NOT cry wolf: an ordinary unreadable directory is not a
	// Trash, so the warning about a trashed checkout must stay silent.
	if strings.Contains(out, "is a Trash") {
		t.Errorf("an ordinary refusal was reported as a Trash:\n%s", out)
	}
	// --verbose names the path, which is what makes the number auditable.
	if _, v, _ := exec1(t, "scan", "--verbose", root); !strings.Contains(v, locked) {
		t.Errorf("--verbose does not name the refused directory:\n%s", v)
	}
}

// ⭐ The other direction, and the reason this split exists: a Trash among the
// refusals is a REAL gap in the verdict, because a checkout moved there keeps
// its .git/config and its remote URL.
func TestARefusedTrashIsCalledOutAgainstTheVerdict(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	repo(t, filepath.Join(root, "fine"), "https://github.com/o/r.git")
	trash := filepath.Join(root, ".Trash")
	if err := os.MkdirAll(filepath.Join(trash, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(trash, 0o000); err != nil {
		t.Skip("cannot make a directory unreadable here")
	}
	t.Cleanup(func() { _ = os.Chmod(trash, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}

	_, out, _ := exec1(t, "scan", root)
	for _, want := range []string{"is a Trash", "does not speak for it", trash} {
		if !strings.Contains(out, want) {
			t.Errorf("a refused Trash is not called out (%q):\n%s", want, out)
		}
	}
	// ⛔ And it must not be printed twice when --verbose already lists it: a
	// path repeated reads as two findings.
	_, v, _ := exec1(t, "scan", "--verbose", root)
	if n := strings.Count(v, trash); n != 1 {
		t.Errorf("the Trash appears %d times under --verbose, want 1:\n%s", n, v)
	}
}

// ⛔⛔ THE TIMEOUT BRANCH IS REACHABLE BY NO TEST THAT WALKS A FILESYSTEM. It
// needs a network or device mount that actually times out, and a branch no test
// can reach is a branch nobody has checked — which is how the defect this whole
// report fixes got in. That is why reportRefusals takes Stats: with the numbers
// passed in, every category has a case.
//
// The numbers below are the ones measured on 2026-10-18 on a home directory of
// 1.38M directories, classified by errno:
//
//	EPERM    136   e.g. ~/.Trash: operation not permitted
//	EACCES     1   e.g. …/Java-Updater/…/Version 17: permission denied
//	ETIMEDOUT 106   e.g. …/CloudStorage/MacDroid-…/.Trash: fdopendir: operation timed out
//
// ⛔ Those 106 were GUESSED to be vanished entries, and written up as such
// before being measured. They are a phone mounted over a network filesystem,
// which is the opposite conclusion: a vanished directory cannot hold anything,
// while one that timed out may hold everything and a later run may read it.
func TestTheRefusalReportNamesEveryCategory(t *testing.T) {
	st := credfix.Stats{
		Repos:              2555,
		Unique:             2293,
		DenyErrors:         243,
		DeniedByPolicy:     136,
		DeniedByPermission: 1,
		DeniedByTimeout:    106,
		Denied: []string{
			"/Users/x/.Trash",
			"/Users/x/Library/CloudStorage/MacDroid-POCO/.Trash",
			"/Users/x/Library/Messages",
		},
	}
	var b bytes.Buffer
	reportRefusals(&b, st, false)
	out := b.String()
	for _, want := range []string{
		"skipped 243 director(ies) that refused listing",
		"136 the system refuses on policy",
		"1 for ordinary permissions a person could grant",
		"106 that did not answer in time",
		"did not answer in time — a network or device mount, not a refusal",
		"a later run may read them",
		// Two Trashes, so the wording must be plural — it was written singular
		// before anybody counted.
		"2 of them are Trashes",
		"does not speak for them",
		"--verbose names them",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	// A Messages store is NOT named as a possible checkout: that is the whole
	// point of separating the reassuring refusals from the one that is not.
	if strings.Contains(out, "Library/Messages\n") && !strings.Contains(out, "--verbose") {
		t.Errorf("a privacy container was listed as if it could hold a checkout:\n%s", out)
	}
}

// ⛔ An "other" bucket that only COUNTS is the unexplained number the whole
// report replaced, so it must carry an example. This is also what revealed the
// timeouts above: the example named them.
func TestTheOtherBucketCarriesAnExample(t *testing.T) {
	st := credfix.Stats{
		DenyErrors:         5,
		DeniedOther:        5,
		DeniedOtherExample: "/some/where: fdopendir /some/where: operation timed out",
	}
	var b bytes.Buffer
	reportRefusals(&b, st, false)
	out := b.String()
	if !strings.Contains(out, "5 for another reason") {
		t.Errorf("the other bucket is not counted:\n%s", out)
	}
	if !strings.Contains(out, "operation timed out") {
		t.Errorf("the other bucket carries no example, so its number is unexplained:\n%s", out)
	}
}

// ⭐ A vanished entry is NOT a refusal and must not be counted as one: nothing
// can be in a directory that no longer exists, and lumping the two together
// dilutes the refusals that do mean something.
func TestAVanishedEntryIsSaidApartFromTheRefusals(t *testing.T) {
	var b bytes.Buffer
	reportRefusals(&b, credfix.Stats{Vanished: 7}, false)
	out := b.String()
	if !strings.Contains(out, "7 entr(ies) that were gone") {
		t.Errorf("a vanished entry is not reported:\n%s", out)
	}
	if strings.Contains(out, "refused listing") {
		t.Errorf("a vanished entry was reported as a refusal:\n%s", out)
	}
}
