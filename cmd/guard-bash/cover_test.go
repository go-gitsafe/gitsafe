package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// A stdin that cannot be read is no opinion: nothing is written, exit 0.
func TestAnUnreadableStdinIsNoOpinion(t *testing.T) {
	var out strings.Builder
	if code := run(failingReader{}, &out); code != 0 || out.String() != "" {
		t.Errorf("run = %d, %q", code, out.String())
	}
}

// main hands run's status to the exit function, reading the real stdin.
func TestMainExitsWithRunsStatus(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(`{"tool_input":{"command":"ls"}}`); err != nil {
		t.Fatal(err)
	}
	w.Close()
	oldIn, oldExit, oldOut := os.Stdin, osExit, os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	os.Stdin, os.Stdout = r, devnull
	got := -1
	osExit = func(c int) { got = c }
	defer func() { os.Stdin, osExit, os.Stdout = oldIn, oldExit, oldOut }()
	main()
	if got != 0 {
		t.Errorf("main exited with %d", got)
	}
}

// A deny that cannot be marshalled still exits 0 and says nothing: the exit
// code reports whether the guard worked.
func TestADenyThatCannotBeMarshalledIsSilent(t *testing.T) {
	old := marshal
	marshal = func(any) ([]byte, error) { return nil, errors.New("no") }
	defer func() { marshal = old }()
	var out strings.Builder
	if code := deny(&out, "x"); code != 0 || out.String() != "" {
		t.Errorf("deny = %d, %q", code, out.String())
	}
}

// Every rule the hook runs refuses its own shape, with its own advice, and
// stays silent when its escape variable is set (where it has one).
func TestEachRuleDeniesThroughTheHook(t *testing.T) {
	for _, k := range []string{"GITSAFE_ALLOW_DISCARD", "GITSAFE_ALLOW_PIPE_STATUS", "GITSAFE_ALLOW_SIGPIPE", "GITSAFE_ALLOW_SHELLWORK", "GITSAFE_ALLOW_VOLATILE", "GITSAFE_ALLOW_STALE_BASE"} {
		t.Setenv(k, "")
	}
	t.Setenv("SHELL", "/bin/zsh")
	for name, tc := range map[string]struct{ cmd, want string }{
		"bypass":     {`cd /tmp && git push --force origin main`, "Refused"},
		"discard":    {`git checkout -- .`, "Refused"},
		"pipestatus": {`go build ./... | head -5; echo status=$?`, "Refused"},
		"stalebase":  {`git checkout -b work origin/main`, "Refused"},
		"sigpipe":    {`timeout 400 go mod tidy 2>&1 | head -5`, "Refused"},
		"testrun":    {`go test -run 'TestA|TestB' ./x`, "Use instead"},
		"chainedgate": {`prbody /b.md --repo o/r; gh pr create --repo o/r --title t --body-file /b.md`,
			"Use instead"},
		"shellwork": {`awk '{print}' f`, "Refused"},
	} {
		out, _ := drive(t, `{"tool_input":{"command":`+jsonString(tc.cmd)+`}}`)
		if !strings.Contains(out, `"deny"`) || !strings.Contains(out, tc.want) {
			t.Errorf("%s: %q not denied with %q:\n%s", name, tc.cmd, tc.want, out)
		}
	}
}
