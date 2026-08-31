package main

import (
	"os/exec"
	"testing"
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
