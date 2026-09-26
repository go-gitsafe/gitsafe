package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	for _, want := range []string{
		"remote.origin.url",                       // which key
		"GitHub classic personal access token",    // what kind
		"sha256:",                                 // told apart from another
		"github.com",                              // where
		"walked",                                  // and what was walked
		"found 2 checkouts",                       // counted, not implied
		"1 of them carry a credential",            //
		"would become https://github.com/o/r.git", // and what to do
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
	if err := os.MkdirAll(filepath.Join(root, "broken", ".git"), 0o755); err != nil {
		t.Fatal(err)
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
