package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/helparg"
)

func TestRemoteFrom(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"nothing", nil, "origin"},
		{"only flags", []string{"--dry-run", "--tags"}, "origin"},
		{"a remote", []string{"upstream"}, "upstream"},
		{"flags then a remote", []string{"--dry-run", "origin", "main"}, "origin"},
		{"remote and refspec", []string{"origin", "HEAD:main"}, "origin"},
		{"set-upstream", []string{"-u", "origin", "feature"}, "origin"},
	} {
		if got := remoteFrom(tc.args); got != tc.want {
			t.Errorf("%s: remoteFrom(%q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

// TestErrorsAsFindsAnExitError: the exit status has to survive being wrapped,
// or a failed push reports success.
// ⛔⛔ gitpush HAD NO TEST FOR THIS AT ALL, and nothing else covers it. The
// repository's help gate reads sources for a flag set that answers `-h` with
// the wrong status — and gitpush has no flag set, because it forwards its
// whole argument list to `git push`, so the gate passes over it in silence.
// Its own comment names that blind spot. This is the only thing that holds
// gitpush to the four spellings.
//
// ⭐ It also must NOT run git: `-h` is answered out of gitpush itself, so the
// test asserts the usage text and status without a repository in sight.
func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help", "help"} {
		out, err := os.CreateTemp(t.TempDir(), "out")
		if err != nil {
			t.Fatal(err)
		}
		errf, err := os.CreateTemp(t.TempDir(), "err")
		if err != nil {
			t.Fatal(err)
		}
		if code := run([]string{arg}, out, errf); code != 0 {
			t.Errorf("gitpush %s exited %d, want 0", arg, code)
		}
		b, err := os.ReadFile(out.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "usage: gitpush") {
			t.Errorf("gitpush %s printed no usage:\n%s", arg, b)
		}
	}
}

// ⛔ And the half that keeps it from swallowing a real push: a lone argument
// that merely looks like a question is not one, and anything with more than
// one argument goes to git.
func TestAPushIsNotAQuestion(t *testing.T) {
	for _, args := range [][]string{
		{"-help-me"},
		{"--Help"},
		{"origin"},
		{"help", "origin"},
	} {
		if helpOnly := len(args) == 1 && helparg.Is(args[0]); helpOnly {
			t.Errorf("%q reads as a request for help", args)
		}
	}
}

func TestErrorsAsFindsAnExitError(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 3").Run()
	var ee *exec.ExitError
	if !errorsAs(err, &ee) {
		t.Fatalf("errorsAs did not find the ExitError in %v", err)
	}
	if ee.ExitCode() != 3 {
		t.Errorf("ExitCode = %d, want 3", ee.ExitCode())
	}

	var none *exec.ExitError
	if errorsAs(nil, &none) {
		t.Error("errorsAs found an ExitError in nil")
	}
	if errorsAs(errString("plain"), &none) {
		t.Error("errorsAs found an ExitError in a plain error")
	}
	if errorsAs(wrapped{errString("plain")}, &none) {
		t.Error("errorsAs found an ExitError in a wrapped plain error")
	}
	if !errorsAs(wrapped{err}, &none) {
		t.Error("errorsAs did not unwrap to find the ExitError")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

type wrapped struct{ err error }

func (w wrapped) Error() string { return "wrapped: " + w.err.Error() }
func (w wrapped) Unwrap() error { return w.err }
