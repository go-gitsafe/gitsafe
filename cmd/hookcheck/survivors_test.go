package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These hold refusals that a mutation sweep found nothing guarding. Each one
// asserts the REASON an error came back, because two different refusals often
// return an error and `err != nil` reads them as one answer.

// noPanic fails the test, by assertion, when f panics: a panic is not a guard,
// it is the next change's silent survivor.
func noPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fixedClock(t *testing.T) {
	t.Helper()
	old := timeNow
	timeNow = func() time.Time { return time.Unix(0, 0).UTC() }
	t.Cleanup(func() { timeNow = old })
}

// ask answers "refuse" only for a deny, in either case, and nothing else: the
// other tests never asked a refusing hook through ask and read the word back.
func TestAskReadsADenyAndOnlyADeny(t *testing.T) {
	script := func(t *testing.T, stdout string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "hook")
		body := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '" + stdout + "'\n"
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, c := range []struct{ out, want string }{
		{`{"hookSpecificOutput":{"permissionDecision":"deny"}}`, "refuse"},
		{`{"permissionDecision":"DENY"}`, "refuse"},
		{`{"permissionDecision":"allow"}`, "allow"},
		{`denying nothing`, "allow"}, // the word without its quotes is not a decision
		{``, "allow"},
	} {
		if got := ask(script(t, c.out), "git status", false); got != c.want {
			t.Errorf("hook printing %q: ask = %q, want %q", c.out, got, c.want)
		}
	}
	if got := ask(fakeHook(t, "refuser", "refuse"), "git status", true); got != "refuse" {
		t.Errorf("a background ask of a refusing hook = %q", got)
	}
}

// short on a blank command returns it whole instead of indexing an empty
// slice. The case was held only by a panic.
func TestShortOnABlankCommandDoesNotPanic(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		noPanic(t, "short("+in+")", func() {
			if got := short(in); got != in {
				t.Errorf("short(%q) = %q, want it unchanged", in, got)
			}
		})
	}
}

// With no -settings and no home directory, there is no path to choose: that is
// an error of its own. Without the guard the relative path ".claude/..." is
// read instead and the run ends on "no Bash guard is wired", a different and
// misleading reason.
func TestNoHomeDirectoryIsNamedAsSuch(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("this platform resolves a home directory without the variables")
	}
	var out, errb bytes.Buffer
	if code := check(false, "", &out, &errb); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if strings.Contains(errb.String(), "no Bash guard") || out.Len() != 0 {
		t.Errorf("it went on to read a path it never had:\nout: %s\nerr: %s", out.String(), errb.String())
	}
	if !strings.HasPrefix(errb.String(), "hookcheck:") {
		t.Errorf("stderr = %q", errb.String())
	}
}

// A failure to WRITE during -install stops there. If it did not, the file that
// could not be changed would be read back, found to hold no guard, and the run
// would say "no Bash guard is wired" instead of what went wrong.
// The same arrangement holds save's backup refusal: the file is left as it was.
func TestInstallStopsAtTheFirstWriteFailure(t *testing.T) {
	fixedClock(t)
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The backup's name is taken by a directory, so copying the file to it fails.
	if err := os.Mkdir(p+".bak-19700101-000000", 0o700); err != nil {
		t.Fatal(err)
	}

	if err := wire(p); err == nil {
		t.Error("wire must refuse when the backup cannot be written")
	}
	if got := readFile(t, p); got != "{}" {
		t.Errorf("the settings changed although the backup failed: %q", got)
	}

	var out, errb bytes.Buffer
	if code := check(true, p, &out, &errb); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if strings.Contains(errb.String(), "no Bash guard") {
		t.Errorf("install went on after its write failed: %s", errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("nothing may be reported after a failed install: %s", out.String())
	}
}

// load says the READ failed, not that the JSON was empty. Without the guard
// the nil bytes are parsed and the error is "unexpected end of JSON input",
// which blames the file's content for a failure to open it.
func TestLoadNamesAReadFailure(t *testing.T) {
	dir := t.TempDir() // a directory cannot be read as a file
	_, err := load(dir)
	if err == nil {
		t.Fatal("a directory is not a settings file")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a read failure must not read as an absent file: %v", err)
	}
	var se *json.SyntaxError
	if errors.As(err, &se) || strings.Contains(err.Error(), "unexpected end of JSON") {
		t.Errorf("the failure was reported as bad JSON: %v", err)
	}
	if _, err := bashHooks(dir); err == nil {
		t.Error("bashHooks must pass the read failure on")
	}
}

// wire stops on settings it cannot parse and leaves them untouched: it never
// replaces a file it could not read with a fresh one.
func TestWireRefusesSettingsThatAreNotJSON(t *testing.T) {
	fixedClock(t)
	p := filepath.Join(t.TempDir(), "settings.json")
	const bad = "{not json"
	if err := os.WriteFile(p, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	noPanic(t, "wire", func() {
		err := wire(p)
		if err == nil {
			t.Fatal("wire must refuse a file it cannot parse")
		}
		if !strings.Contains(err.Error(), p) {
			t.Errorf("the error must name the file: %v", err)
		}
	})
	if got := readFile(t, p); got != bad {
		t.Errorf("the file was rewritten: %q", got)
	}
	if m, _ := filepath.Glob(p + ".bak-*"); len(m) != 0 {
		t.Errorf("a backup of an unreadable file was made: %v", m)
	}
}

// save refuses a document it cannot encode, and writes nothing. Without the
// guard the nil encoding is written and the file is replaced by a newline.
func TestSaveRefusesADocumentItCannotEncode(t *testing.T) {
	fixedClock(t)
	p := filepath.Join(t.TempDir(), "settings.json")
	err := save(p, settingsDoc{"x": make(chan int)})
	var ue *json.UnsupportedTypeError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want the encoder's refusal", err)
	}
	if _, serr := os.Stat(p); !errors.Is(serr, fs.ErrNotExist) {
		t.Errorf("a file was written for a document that could not be encoded: %v", serr)
	}

	// An existing file keeps its content too.
	if err := os.WriteFile(p, []byte(`{"keep":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := save(p, settingsDoc{"x": make(chan int)}); err == nil {
		t.Fatal("must still refuse")
	}
	if got := readFile(t, p); got != `{"keep":1}` {
		t.Errorf("the settings were overwritten: %q", got)
	}
}

// save fails at MKDIR when the directory cannot be made. The reason is the
// point: without the guard the write fails next and also returns an error, so
// only the operation named in it tells the two apart.
func TestSaveNamesTheDirectoryItCouldNotMake(t *testing.T) {
	fixedClock(t)
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := save(filepath.Join(notADir, "sub", "settings.json"), settingsDoc{})
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a path error", err)
	}
	if pe.Op != "mkdir" {
		t.Errorf("failed at %q, want mkdir: %v", pe.Op, err)
	}
}
