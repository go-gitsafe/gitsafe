// Package testrun answers one question: is this `go test` going to report `ok`
// about tests that are not the ones being checked?
//
// # Why it exists
//
// `ok` is about the tests that RAN, not about the one you asked for. Measured
// on 2026-11-09 in go-zpkg/zbk, while checking a README gate:
//
//	$ go test -run 'TestREADME|TestUsage|TestMain' ./cmd/zbk
//	ok  	github.com/go-zpkg/zbk/cmd/zbk	0.159s
//
// The gate was `TestTheREADMECountsTheSubcommandsItHas`, and `TestREADME`
// matched NOTHING. The other two alternatives matched four tests — the same run
// with -v shows TestMainMultiCallDispatch, TestUsageAndErrors, TestMainSeam and
// TestRunTestUsage — so the selection was not empty, `go test` had nothing to
// warn about, and the `ok` was TRUE. The gate had simply not run. Named
// correctly it went red on the first attempt:
//
//	39 subcommands, and numberWords does not spell that
//
// The only thing that gave it away was the TIME: 0.159s for what should have
// read two files and a dispatch switch. That is not a control, it is a
// coincidence somebody noticed.
//
// ⛔ AND GO ONLY WARNS WHEN THE SELECTION IS WHOLLY EMPTY, and even then exits
// 0:
//
//	$ go test -run NoSuchTestNameAtAll ./cmd/covblocks ; echo $?
//	ok  	…/cmd/covblocks	0.051s [no tests to run]
//	0
//
// So the information exists in both cases and the VERDICT does not.
//
// # What it refuses, stated as the ACT
//
// Filtering a test run by hand, where the filter's verdict is not checked.
//
//	go test -run TestFoo ./pkg        the filter whose match nobody verifies
//	go test -run=TestFoo ./...        the same, other spelling
//	go test -test.run=TestFoo         the compiled binary's spelling
//
// # What it does NOT refuse, and that half is the point
//
//	go test ./...                     no filter, so no alternative can miss
//	go test -count=1 -race ./pkg      the same
//	gotest -run TestFoo ./pkg         the program that checks the filter
//	go test -run TestFoo --help       asking what it does is not doing it
//
// A guard is deleted by its FALSE positives, and this repository has the record
// of fifteen from an earlier rule. `go test` without `-run` is the overwhelming
// majority of runs and is left entirely alone: with no filter there is no
// alternative that can match nothing.
//
// # Why a wrapper rather than a lint
//
// Whether a pattern matched cannot be known before running it, so there is
// nothing to check statically. `gotest` runs the same command, adds `-v` when
// it must (and says so), and compares each ALTERNATIVE of the pattern against
// the tests that actually ran — the three that matched are not what is at
// issue, the one that did not is. Same shape as `gitpush` wrapping `git push`:
// the work is identical, the verdict is not.
package testrun

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing found.
type Finding struct {
	// Kind is the act, in the words of the rule it breaks.
	Kind string
	// Match is the text that showed it, so a refusal points rather than asserts.
	Match string
	// Instead is what to run for this particular kind.
	Instead string
}

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Kind != "" }

// Why names the act.
func (f Finding) Why() string {
	return "it filters a test run and does not check the filter: " + f.Kind
}

func (f Finding) String() string {
	if !f.Found() {
		return ""
	}
	return "it matched `" + f.Match + "`"
}

// Advice is the general remedy; Finding.Instead is the specific one.
const Advice = "`gotest` takes exactly the arguments `go test` takes and runs the " +
	"same command; it adds -v when -run is present (and says so), then checks that " +
	"each ALTERNATIVE of the pattern matched a test that ran. An alternative that " +
	"matched nothing exits 1 and is named. Install it with " +
	"`GOBIN=~/.local/bin go install github.com/go-fleettools/fleettools/cmd/gotest@latest`."

// runFlags are the spellings `go test` accepts for the filter, including the
// one a compiled test binary takes.
var runFlags = []string{"-run", "--run", "-test.run", "--test.run"}

// Check says whether cmd filters a test run without checking the filter.
func Check(cmd string) Finding {
	for _, st := range shellcmd.Statements(shellcmd.StripQuotedHeredocs(cmd)) {
		for _, stage := range shellcmd.Stages(st) {
			if f := checkStatement(stage); f.Found() {
				return f
			}
		}
	}
	return Finding{}
}

// checkStatement asks about one simple command.
func checkStatement(st string) Finding {
	words := strings.Fields(st)
	// Skip any leading VAR=value assignments, the way a shell does: a rule that
	// read the first word literally would miss `GOFLAGS=-count=1 go test …`.
	for len(words) > 0 && isAssignment(words[0]) {
		words = words[1:]
	}
	if len(words) < 2 || base(words[0]) != "go" || words[1] != "test" {
		return Finding{}
	}
	// ⛔ ASKING A COMMAND WHAT IT DOES IS NOT DOING IT. The sibling rule in this
	// repository refused `gh pr merge --help` until its corpus said so, and that
	// is the kind of false refusal that gets a guard deleted.
	for _, w := range words[2:] {
		if w == "--help" || w == "-h" {
			return Finding{}
		}
	}
	for _, w := range words[2:] {
		for _, flag := range runFlags {
			if w == flag || strings.HasPrefix(w, flag+"=") {
				return Finding{
					Kind:    "a `" + flag + "` whose match nobody verifies",
					Match:   short(st),
					Instead: "gotest " + strings.Join(words[2:], " "),
				}
			}
		}
	}
	return Finding{}
}

// isAssignment reports whether a word is a leading NAME=value, which a shell
// applies to the command's environment rather than treating as the command.
func isAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := w[j]
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(j > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// base is the command name without its directory, so an absolute path to the
// toolchain is judged the same as the bare word.
func base(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// short trims a statement for a message, because a refusal that prints a
// screenful is a refusal nobody reads.
func short(s string) string {
	s = strings.TrimSpace(s)
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
