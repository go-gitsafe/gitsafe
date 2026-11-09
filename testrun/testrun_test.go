package testrun

import (
	"strings"
	"testing"
)

func TestRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
	}{
		// The command that printed `ok` while the gate it was checking had not
		// run, on 2026-11-09 in go-zpkg/zbk.
		{"the one that cost a gate", "go test -run 'TestREADME|TestUsage|TestMain' ./cmd/zbk"},
		{"equals", "go test -run=TestFoo ./pkg"},
		{"two dashes", "go test --run TestFoo ./pkg"},
		{"the compiled binary's spelling", "go test -test.run=TestFoo"},
		{"beside other flags", "go test -count=1 -race -run TestFoo ./..."},
		// A leading assignment is the shell's, not the command's.
		{"after an assignment", "GOFLAGS=-mod=mod go test -run TestFoo ./pkg"},
		// An absolute toolchain is the same command.
		{"an absolute go", "/usr/local/go/bin/go test -run TestFoo ./pkg"},
		// Inside a longer line, which is how these are actually typed.
		{"after a cd", "cd /tmp/x && go test -run TestFoo ./pkg > /tmp/o.txt 2>&1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := Check(tc.cmd)
			if !f.Found() {
				t.Fatalf("not refused: %s", tc.cmd)
			}
			if !strings.Contains(f.Instead, "gotest") {
				t.Errorf("the refusal does not name the program: %q", f.Instead)
			}
			if !strings.Contains(f.Why(), "does not check the filter") {
				t.Errorf("Why does not name the act: %q", f.Why())
			}
			if !strings.Contains(f.String(), "it matched") {
				t.Errorf("String does not point at the text: %q", f.String())
			}
		})
	}
}

// ⛔ A GUARD'S FALSE POSITIVES ARE WHAT GET IT DELETED, and this repository has
// the record: fifteen wrong refusals from an earlier rule. `go test` with no
// filter is the overwhelming majority of runs and is left entirely alone —
// with no filter there is no alternative that can match nothing.
func TestAllows(t *testing.T) {
	for _, cmd := range []string{
		"go test ./...",
		"go test -count=1 -race ./cmd/zbk",
		"go test -covermode=atomic -coverprofile=cov.out ./...",
		"go test ./... > /tmp/out.txt 2>&1",
		// The program that does check the filter.
		"gotest -run TestFoo ./pkg",
		"gotest -run 'TestA|TestB' ./...",
		// Asking what it does is not doing it.
		"go test -run TestFoo --help",
		"go test --help",
		// Other go verbs, including ones whose flags look similar.
		"go build ./...",
		"go vet ./...",
		"go run ./internal/probe",
		"go version -m ~/.local/bin/zbk",
		"go install github.com/go-fleettools/fleettools/cmd/gotest@v0.95.0",
		// A different program that happens to take -run.
		"zbk lint --dir . --require-standalone",
		"mutate -run TestFoo",
		// The words, not the act: this phrase appears in commit messages and in
		// prose about this very rule.
		`git commit -m "go test -run is refused now"`,
		// Not a command at all.
		"",
		"go",
		"go test",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("refused legitimate work: %s\n  %s", cmd, f.Why())
		}
	}
}

func TestIsAssignment(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"GOFLAGS=-mod=mod", true},
		{"CGO_ENABLED=0", true},
		{"a=1", true},
		{"_X=1", true},
		{"=1", false},
		{"go", false},
		{"./x=y", false},
		{"-run=TestX", false},
	} {
		if got := isAssignment(tc.in); got != tc.want {
			t.Errorf("isAssignment(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestShortTrims(t *testing.T) {
	long := "go test -run Test" + strings.Repeat("X", 200) + " ./pkg"
	f := Check(long)
	if !f.Found() {
		t.Fatal("not refused")
	}
	if !strings.HasSuffix(f.Match, "…") {
		t.Errorf("a long statement was not trimmed: %q", f.Match)
	}
}

func TestZeroValueSaysNothing(t *testing.T) {
	var f Finding
	if f.Found() {
		t.Error("the zero value reports a refusal")
	}
	if f.String() != "" {
		t.Errorf("the zero value prints %q", f.String())
	}
}

// The advice names the program and how to get it: a refusal that names a
// command nobody can run is worse than no refusal.
func TestAdviceNamesTheProgramAndHowToInstallIt(t *testing.T) {
	for _, want := range []string{"gotest", "go install", "fleettools"} {
		if !strings.Contains(Advice, want) {
			t.Errorf("Advice does not mention %q: %q", want, Advice)
		}
	}
}
