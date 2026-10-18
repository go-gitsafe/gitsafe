package sigpipe

import (
	"strings"
	"testing"
)

// ⛔ The first case is the command REALLY typed on 2026-09-06, which produced a
// written conclusion that a third party's module was broken. It was not.
func TestItRefusesAPipelineThatAbandonsTheWork(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"the one that caused a false conclusion",
			`timeout 400 go mod tidy 2>&1 | head -5`},
		{"without the timeout wrapper",
			`go mod tidy | head -5`},
		{"an environment assignment in front",
			`GOWORK=off GOFLAGS=-mod=mod go build ./... | head -20`},
		{"the consumer is in the MIDDLE of the pipeline",
			`go build ./cmd/x 2>&1 | head -20 | sed 's/^/  /'`},
		{"grep that returns at the first match",
			`go test ./... | grep -q FAIL`},
		{"grep bounded to a count of matches",
			`go test ./... | grep -m 3 FAIL`},
		{"a bundled short flag that includes q",
			`go build ./... | grep -iq error`},
		{"a fetch, not a build",
			`git clone https://example.invalid/x | head -1`},
		{"make, where every target counts",
			`make 2>&1 | head -5`},
		{"curl told to write a file",
			`curl -o out.bin https://example.invalid/x | head -1`},
		{"an absolute path to the consumer",
			`go mod download | /usr/bin/head -2`},
		{"the second statement is the offender",
			"go list ./... | head -3\ngo mod tidy | head -3"},
	} {
		f := Check(c.cmd)
		if !f.Found() {
			t.Errorf("%s: NOT refused: %q", c.name, c.cmd)
			continue
		}
		if f.String() == "" {
			t.Errorf("%s: refused with no explanation", c.name)
		}
	}
}

// ⭐ The half that keeps the guard from being worked around. A guard that
// refuses ordinary work is a guard people learn to avoid, and it then protects
// nothing — this repository has already watched that happen to a rule that
// matched "push" too broadly.
func TestItAllowsWhatIsNotTheMistake(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"a file can be read again for free", `cat f.txt | head -5`},
		{"so can a directory listing", `ls -la | head -20`},
		{"a read-only git subcommand", `git log --oneline | head -20`},
		{"git status is a read", `git status --porcelain | head`},
		{"git diff is a read", `git diff | head -40`},
		{"go list only reads", `go list ./... | head -5`},
		{"go env only reads", `go env | head -3`},
		{"go version only reads", `go version -m ./bin/x | head -3`},
		{"tail needs the end, so the build finishes", `go build ./... | tail -5`},
		{"grep -c reads every line", `go test ./... | grep -c PASS`},
		{"plain grep reads every line", `go build ./... 2>&1 | grep error`},
		{"wc needs everything", `go test ./... | wc -l`},
		{"sed streams to the end", `go build ./... 2>&1 | sed 's/^/  /'`},
		{"sort needs everything", `go list ./... | sort | uniq`},
		{"the correct form, with no pipe at all", `go mod tidy > /tmp/t.txt 2>&1`},
		{"no pipeline", `go mod tidy`},
		{"head with no producer before it", `head -5 /tmp/t.txt`},
		{"curl that only reads", `curl -sS https://example.invalid/x | head -1`},
		{"an unknown producer is allowed through — the documented blind spot",
			`somehomegrowntool --sweep | head -5`},
		{"writing about it in single quotes", `echo 'go mod tidy | head -5'`},
		{"writing about it in a quoted heredoc", `cat > note.md <<'EOF'
go mod tidy | head -5
EOF`},
		{"a pipe inside a string is not a pipeline", `go list ./... > f; grep "a|b" f`},
		{"empty command", ``},
		{"a different go subcommand that is a read", `go doc fmt.Println | head -5`},
	} {
		if f := Check(c.cmd); f.Found() {
			t.Errorf("%s: wrongly refused (%s): %q", c.name, f, c.cmd)
		}
	}
}

// The refusal must NAME both sides, so it points at something rather than
// asserting.
func TestTheRefusalNamesBothSides(t *testing.T) {
	f := Check(`timeout 400 go mod tidy 2>&1 | head -5`)
	if !f.Found() {
		t.Fatal("not refused")
	}
	if f.Producer == "" || f.Consumer == "" || f.Pipeline == "" {
		t.Errorf("the refusal does not name both sides: %+v", f)
	}
	for _, want := range []string{"head -5", "go mod tidy", "SIGPIPE"} {
		if !strings.Contains(f.String()+" "+f.Why(), want) {
			t.Errorf("the explanation does not say %q: %s / %s", want, f, f.Why())
		}
	}
}

// ⛔ The two rules are SIBLINGS and must not be confused: this one is about work
// abandoned, pipestatus is about a status read from the wrong command. A
// pipeline can do either, both or neither, so each case below belongs to exactly
// one of them.
func TestItIsNotTheOtherRule(t *testing.T) {
	// Work finishes, but a status is read from tail: pipestatus's case, not ours.
	if f := Check(`go build ./... | tail -5 ; echo "status=$?"`); f.Found() {
		t.Errorf("a finished build with a wrong status read is pipestatus's case, not this one: %s", f)
	}
	// Work is abandoned and nobody reads a status: ours alone.
	if f := Check(`go mod tidy | head -5`); !f.Found() {
		t.Error("an abandoned producer with no status read is this rule's case and nobody else's")
	}
}
