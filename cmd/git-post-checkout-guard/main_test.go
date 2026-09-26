package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/credfix"
	"github.com/go-gitsafe/gitsafe/credurl"
)

// fakeToken is SHAPED like a GitHub classic personal access token and is not one.
var fakeToken = "gh" + "p_" + strings.Repeat("Ab3", 12)

func exec1(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
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

// TestItStripsWhatACloneWroteAndFails, on a real repository, entered the way the
// hook is entered: with the work tree as the working directory.
//
// Both halves are asserted. A hook that stripped the credential and exited 0
// would hide the only moment a person is looking; a hook that shouted and left
// the token in the file would leave every later fetch printing it.
func TestItStripsWhatACloneWroteAndFails(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", ".")
	gitIn(t, dir, "config", "--local", "remote.origin.url", "https://"+fakeToken+"@github.com/o/r.git")
	t.Chdir(dir)

	code, _, errb := exec1(t, "HEAD", "HEAD", "1")
	if code == 0 {
		t.Error("exit 0: the message would scroll away with nothing to stop on")
	}
	if strings.Contains(errb, fakeToken) {
		t.Error("the hook printed the credential it is complaining about")
	}
	for _, want := range []string{
		"carried a CREDENTIAL",
		"GitHub classic personal access token",
		"read back: now https://github.com/o/r.git",
		"DISCLOSED",
		"cannot stop a clone",
	} {
		if !strings.Contains(errb, want) {
			t.Errorf("the message does not say %q:\n%s", want, errb)
		}
	}
	if got := gitIn(t, dir, "config", "--local", "--get", "remote.origin.url"); got != "https://github.com/o/r.git" {
		t.Errorf("the credential is still in the configuration: %q", got)
	}
}

// TestAnOrdinarySshCheckoutIsSilent. This runs on EVERY checkout on the machine,
// so a false positive is not a nuisance, it is the end of the hook: it would be
// uninstalled within the hour, and then nothing guards anything.
func TestAnOrdinarySshCheckoutIsSilent(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", ".")
	gitIn(t, dir, "config", "--local", "remote.origin.url", "ssh://git@github.com/o/r.git")
	gitIn(t, dir, "config", "--local", "remote.up.url", "git@plmlab.math.cnrs.fr:team/repo")
	t.Chdir(dir)

	code, out, errb := exec1(t, "HEAD", "HEAD", "1")
	if code != 0 {
		t.Errorf("exit = %d on an ordinary ssh checkout:\n%s%s", code, out, errb)
	}
	if strings.TrimSpace(out+errb) != "" {
		t.Errorf("it said something about a checkout with no credential in it:\n%s%s", out, errb)
	}
	if got := gitIn(t, dir, "config", "--local", "--get", "remote.origin.url"); got != "ssh://git@github.com/o/r.git" {
		t.Errorf("the ssh URL was rewritten to %q", got)
	}
}

// TestWhatItCouldNotRepairItStillNames: a credential it cannot rewrite must not
// turn into silence.
func TestWhatItCouldNotRepairItStillNames(t *testing.T) {
	restore := repair
	defer func() { repair = restore }()
	repair = func(dir string, write bool) credfix.Report {
		return credfix.Report{Dir: dir, Keys: 3, Leaks: []credfix.Entry{{
			Key:     "url.https://github.com/.insteadof",
			Finding: credurl.Inspect("https://" + fakeToken + "@github.com/"),
			InKey:   true,
		}}}
	}
	code, _, errb := exec1(t, "HEAD", "HEAD", "1")
	if code == 0 {
		t.Error("exit 0 with a credential still in the configuration")
	}
	if !strings.Contains(errb, "STILL THERE") || !strings.Contains(errb, "credscan clean") {
		t.Errorf("it did not say what is left to do:\n%s", errb)
	}
}

// TestTheEscapeOpens. A guard with no way out is a guard that gets uninstalled,
// and then it protects nothing at all.
func TestTheEscapeOpens(t *testing.T) {
	t.Setenv("GITSAFE_ALLOW_URL_CREDENTIAL", "1")
	restore := repair
	defer func() { repair = restore }()
	repair = func(string, bool) credfix.Report {
		t.Error("the escape did not open: the repository was inspected anyway")
		return credfix.Report{}
	}
	if code, _, errb := exec1(t, "HEAD", "HEAD", "1"); code != 0 {
		t.Errorf("exit = %d with the escape set:\n%s", code, errb)
	}
}

// TestAnUnreadableRepositoryIsNotACheckoutFailure: this hook runs on every
// checkout, and one that failed whenever it could not read something would break
// a machine rather than guard it. It says so and gets out of the way; counting
// those is `credscan scan`'s job, and it counts them as unreadable, not clean.
func TestAnUnreadableRepositoryIsNotACheckoutFailure(t *testing.T) {
	restore := repair
	defer func() { repair = restore }()
	repair = func(dir string, write bool) credfix.Report {
		return credfix.Report{Dir: dir, Err: errors.New("no configuration")}
	}
	code, _, errb := exec1(t, "HEAD", "HEAD", "1")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(errb, "cannot read") {
		t.Errorf("it failed silently:\n%s", errb)
	}
}

// TestItChainsToTheRepositorysOwnHook. Setting core.hooksPath globally makes git
// look ONLY there, so a repository's own post-checkout hook stops running the
// moment this is installed.
func TestItChainsToTheRepositorysOwnHook(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", ".")
	gitIn(t, dir, "config", "--local", "remote.origin.url", "https://github.com/o/r.git")
	marker := filepath.Join(dir, "ran")
	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho \"$@\" > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if code, out, errb := exec1(t, "a", "b", "1"); code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out, errb)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the repository's own hook did not run: %v", err)
	}
	if strings.TrimSpace(string(got)) != "a b 1" {
		t.Errorf("the hook was given %q, not its arguments", got)
	}
}

// TestTheChainedHooksRefusalIsPassedOn. If a repository's own hook refuses, the
// refusal must arrive: swallowing it would be this guard silently overruling
// something it knows nothing about.
func TestTheChainedHooksRefusalIsPassedOn(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", ".")
	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho no >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if code, _, errb := exec1(t, "a", "b", "1"); code != 3 {
		t.Errorf("exit = %d, want the chained hook's 3\n%s", code, errb)
	}
}

// TestANonExecutableHookIsNotRun: a file left there by an editor or a template is
// not a hook, and trying to execute it would turn every checkout into an error.
func TestANonExecutableHookIsNotRun(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", ".")
	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if code, _, errb := exec1(t, "a", "b", "1"); code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, errb)
	}
}

// TestOutsideARepositoryItSaysNothing: git runs hooks from a work tree, but this
// is a binary on a PATH and somebody will run it by hand.
func TestOutsideARepositoryItSaysNothing(t *testing.T) {
	hermetic(t)
	t.Chdir(t.TempDir())
	code, out, errb := exec1(t, "a", "b", "1")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(errb, "cannot read") && strings.TrimSpace(out) != "" {
		t.Errorf("unexpected output: %q %q", out, errb)
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b":      "/a/b",
		"/a b":      "'/a b'",
		"/a'b":      `'/a'\''b'`,
		"/Users/me": "/Users/me",
	} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %q, want %q", in, got, want)
		}
	}
}
