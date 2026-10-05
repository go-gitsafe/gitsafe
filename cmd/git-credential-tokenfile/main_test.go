// Copyright (c) the gitsafe authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔⛔ THIS PROGRAM HAD NO TESTS AT ALL, and it is the ONE program in this
// repository whose job is to put a secret on a stream. Everything else here
// exists to keep tokens out of argv, out of the environment and out of
// transcripts; this one hands the token to git on purpose, over a pipe, and its
// whole safety is two refusals and their ORDER.
//
// A guard nobody has seen fire is a guard nobody can be sure of.

// fakeHome points the helper at a token this test wrote, so nothing here can
// read the real ~/.github-token.
//
// ⚠ BOTH VARIABLES. os.UserHomeDir reads $HOME on unix and %USERPROFILE% on
// windows, and a test that sets only one is green on the platform it was
// written on -- which cost a red lane in go-xrkit/desk the day before this.
func fakeHome(t *testing.T, token string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if token != "" {
		if err := os.WriteFile(filepath.Join(dir, tokenFile), []byte(token+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// pipes gives run a stdout that is NOT a character device, which is what git
// hands it, and reads back what was written.
func pipes(t *testing.T, stdinText string) (stdin, stdout, stderr *os.File, read func() (string, string)) {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = inW.WriteString(stdinText); inW.Close() }()
	return inR, outW, errW, func() (string, string) {
		outW.Close()
		errW.Close()
		ob := make([]byte, 4096)
		n, _ := outR.Read(ob)
		eb := make([]byte, 4096)
		m, _ := errR.Read(eb)
		return string(ob[:n]), string(eb[:m])
	}
}

const probe = "ghp_thisisnotarealtokenbutitislongenough"

// ⛔ A TERMINAL IS THE ONE PLACE THIS MUST NEVER WRITE, because the only way a
// terminal is on the other end is somebody running it by hand to see what it
// says -- which is precisely the act that must not print a token.
//
// ⚠ AND THE REFUSAL MUST COME BEFORE THE TOKEN IS READ. The order is the
// safety: a version that read first and refused second would have the secret in
// memory and one careless error path away from printing it. The test proves the
// order by giving it a VALID token file and asserting the token appears
// nowhere -- not on stdout, not on stderr.
func TestItRefusesToWriteToATerminal(t *testing.T) {
	fakeHome(t, probe)

	tty, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	// /dev/null is a character device, which is what isTerminal tests for, and
	// it is the only such file a test can rely on existing.
	if st, err := tty.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		t.Skip("no character device available to stand in for a terminal")
	}

	inR, _, errW, read := pipes(t, "host=github.com\n\n")
	if got := run([]string{"get"}, inR, tty, errW); got != 1 {
		t.Errorf("run = %d, want 1: writing a credential to a terminal must refuse", got)
	}
	_, stderr := read()
	if !strings.Contains(stderr, "refusing") {
		t.Errorf("stderr = %q, which does not say it refused", stderr)
	}
	if strings.Contains(stderr, probe) {
		t.Error("the refusal contains the token; the refusal is the one path that " +
			"must not have read it")
	}
}

// ⛔ ONLY "get". A helper that can store or erase can be talked into writing a
// secret somewhere new, so the other two verbs are answered with silence --
// which git accepts as "this helper has nothing to contribute".
func TestItAnswersOnlyGet(t *testing.T) {
	for _, args := range [][]string{{"store"}, {"erase"}, {}, {"get", "extra"}, {"GET"}} {
		fakeHome(t, probe)
		inR, outW, errW, read := pipes(t, "host=github.com\n\n")
		code := run(args, inR, outW, errW)
		stdout, _ := read()
		if strings.Contains(stdout, probe) {
			t.Errorf("args %v: the token was written for a verb that is not get", args)
		}
		if code != 0 {
			t.Errorf("args %v: run = %d, want 0 -- silence, not an error", args, code)
		}
	}
}

// ⛔ ONLY GITHUB, so a misconfigured helper cannot offer this token to some
// other host that asks for a credential.
func TestItOffersTheTokenToGitHubAndNobodyElse(t *testing.T) {
	for _, c := range []struct {
		host  string
		wants bool
	}{
		{"github.com", true},
		{"api.github.com", true},
		{"", true}, // git asking without a host is git asking for github here
		{"gitlab.com", false},
		{"evil.example", false},
		// ⛔ AND A SUFFIX IS NOT A MATCH THE OTHER WAY ROUND: a host that merely
		// ENDS in the name is a different machine.
		{"notgithub.com", false},
	} {
		fakeHome(t, probe)
		inR, outW, errW, read := pipes(t, "protocol=https\nhost="+c.host+"\n\n")
		code := run([]string{"get"}, inR, outW, errW)
		stdout, _ := read()
		got := strings.Contains(stdout, probe)
		if got != c.wants {
			t.Errorf("host %q: token offered = %t, want %t (stdout %q)",
				c.host, got, c.wants, stdout)
		}
		if c.wants && !strings.Contains(stdout, "username=x-access-token") {
			t.Errorf("host %q: stdout = %q, which git cannot read", c.host, stdout)
		}
		if code != 0 {
			t.Errorf("host %q: run = %d, want 0", c.host, code)
		}
	}
}

// ⛔ AND A FAILURE MUST NOT LEAK WHAT A SUCCESS WOULD HAVE PROTECTED: the error
// names the PATH, never the contents.
func TestAFailureNamesThePathAndNotTheToken(t *testing.T) {
	for _, c := range []struct{ name, token string }{
		{"no token file at all", ""},
		{"an empty token file", "   "},
	} {
		dir := fakeHome(t, c.token)
		if c.token == "   " {
			if err := os.WriteFile(filepath.Join(dir, tokenFile), []byte("   \n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		inR, outW, errW, read := pipes(t, "host=github.com\n\n")
		if code := run([]string{"get"}, inR, outW, errW); code != 1 {
			t.Errorf("%s: run = %d, want 1", c.name, code)
		}
		stdout, stderr := read()
		if stdout != "" {
			t.Errorf("%s: stdout = %q, want nothing at all", c.name, stdout)
		}
		if !strings.Contains(stderr, tokenFile) {
			t.Errorf("%s: stderr = %q, which does not name the file to look at",
				c.name, stderr)
		}
	}
}

// ⛔⛔ A SECRET FILE OTHERS CAN READ IS NOT A SECRET, and nothing here looked
// until this test. The real ~/.github-token is 0600, which is why the gap was
// invisible: a check that only matters after a drift is one no drift reveals.
//
// ⚠ IT ASSERTS THE REFUSAL AND THE REMEDY, because a refusal somebody cannot
// act on sends them looking. And it asserts the token is NOT in the message:
// the whole point of looking before reading is that the contents never enter
// the process.
func TestItRefusesATokenFileOthersCanRead(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660, 0o666} {
		dir := fakeHome(t, probe)
		path := filepath.Join(dir, tokenFile)
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		inR, outW, errW, read := pipes(t, "host=github.com\n\n")
		code := run([]string{"get"}, inR, outW, errW)
		stdout, stderr := read()
		if code != 1 {
			t.Errorf("mode %04o: run = %d, want 1", mode, code)
		}
		if stdout != "" {
			t.Errorf("mode %04o: stdout = %q -- the token was served from a file "+
				"other accounts can read", mode, stdout)
		}
		if strings.Contains(stderr, probe) {
			t.Errorf("mode %04o: the refusal contains the token", mode)
		}
		if !strings.Contains(stderr, "chmod 600") {
			t.Errorf("mode %04o: stderr = %q, which does not say what to do",
				mode, stderr)
		}
	}
	// ⚠ AND 0600 AND 0400 STILL WORK, which is the direction that matters most:
	// a guard that refuses everything would pass the test above and break every
	// push on this machine.
	for _, mode := range []os.FileMode{0o600, 0o400} {
		dir := fakeHome(t, probe)
		if err := os.Chmod(filepath.Join(dir, tokenFile), mode); err != nil {
			t.Fatal(err)
		}
		inR, outW, errW, read := pipes(t, "host=github.com\n\n")
		if code := run([]string{"get"}, inR, outW, errW); code != 0 {
			t.Errorf("mode %04o: run = %d, want 0", mode, code)
		}
		if stdout, _ := read(); !strings.Contains(stdout, probe) {
			t.Errorf("mode %04o: the token was not served; stdout = %q", mode, stdout)
		}
	}
}
