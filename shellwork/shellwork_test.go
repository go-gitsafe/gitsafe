package shellwork

import (
	"strings"
	"testing"
)

// ⛔ EVERY REFUSAL BELOW IS A COMMAND THE OPERATOR ACTUALLY TYPED ON 2026-10-18,
// after reading the rule against it three times and agreeing to it twice. They
// are not hypotheticals, and that is the whole argument for the guard.
func TestItRefusesWorkTheShellShouldNotBeDoing(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"a pipeline, which is where five wrong answers came from",
			`command grep -h "require" go.mod | command cut -d' ' -f1 | command sort -u`},
		{"a pipeline of two",
			`gh run list --json conclusion | head -1`},
		{"sed doing the processing",
			`sed -E "s|^$HOME/||" /tmp/paths.txt`},
		{"awk, which the instruction file names first",
			`go version -m ~/.local/bin/gitpush | awk '/^\tbuild/ {print $3}'`},
		{"awk as the command itself",
			`awk '{print $5, $9}' /tmp/sizes.txt`},
		{"python doing the work",
			`python3 -c "import json;print(1)"`},
		{"perl in place of an edit",
			`perl -pi -e 's/a/b/' README.md`},
		{"a for loop",
			`for i in 1 2 3; do echo "$i"; done`},
		{"a while loop reading a file",
			"while IFS= read -r line; do echo \"$line\"; done < /tmp/list.txt"},
		{"a commit message on stdin rather than in a file",
			"git commit -q -F - <<'MSG'\nsubject\nMSG"},
		{"a pull-request body on stdin",
			`gh pr create --title x --body-file -`},
		{"an interpreter behind a wrapper",
			`command sed -n '1,5p' /tmp/f.txt`},
		{"an absolute path to the interpreter",
			`/usr/bin/awk '{print}' /tmp/f.txt`},
	} {
		f := Check(c.cmd)
		if !f.Found() {
			t.Errorf("%s: NOT refused: %q", c.name, c.cmd)
			continue
		}
		if f.String() == "" || f.Instead == "" {
			t.Errorf("%s: refused with no explanation or no remedy: %+v", c.name, f)
		}
	}
}

// ⛔⛔ THE REFUSAL THAT WAS WRONG, MEASURED 2026-10-31 ON THIS SESSION'S OWN
// WORK. Sending a peer session a note about the cobra migration was refused as
// "a shell loop", because the note contains the English word "while" and, a
// few words later, "do". The rule already knew that writing ABOUT a loop is
// not a loop — its helper simply counted only `'`, so the exemption applied in
// single quotes and not in double ones, while the pipeline rule next door read
// both alike. Two notions of "quoted" in one repository.
//
// A guard that refuses legitimate work teaches people to reach for
// GITSAFE_ALLOW_SHELLWORK=1, and a guard that is routinely switched off is not
// a floor.
func TestProseInDoubleQuotesIsNotShellWork(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"the note that was refused",
			`agentsync tell go-fleettools/fleettools "the call sites break at the migration, ` +
				`while go test stays green because tests do spell their own arguments"`},
		// ⛔ This case said `-F -` at first and was refused for THAT, by the
		// stdin-body rule, correctly. A case that fails for a reason other than
		// the one it is testing proves nothing about the rule under test.
		{"a commit message that warns against a loop",
			`git commit -m "do not write: for f in *; do rm $f; done"`},
		{"the same sentence in single quotes, which always worked",
			`agentsync tell repo 'green while tests do run'`},
		{"a pipe talked about rather than used",
			`agentsync tell repo "redirect rather than | head, which drops the status"`},
		{"an interpreter named in prose",
			`git commit -m "replace the awk one-liner with a Go program"`},
	} {
		if f := Check(c.cmd); f.Found() {
			t.Errorf("%s: REFUSED prose: %s\n  %q", c.name, f.String(), c.cmd)
		}
	}
}

// ⛔⛔ ASKING AN INTERPRETER WHAT IT IS, which this refused as "doing the
// processing" until 2026-10-31. The doctrine it enforces says "invoking a
// program and looking at it is not building a tool" and names `go version`;
// guardscan found this is the ONLY class among the 26 escapes in 443,030
// recorded commands that the rule should not have refused.
func TestAskingAnInterpreterWhatItIsIsReading(t *testing.T) {
	for _, cmd := range []string{
		"ruby -v",
		"/usr/bin/ruby --version",
		"ruby --version > /tmp/v.txt",
		"ruby --version >/tmp/v.txt",
		"python3 --version 2>&1",
		"python3 -V",
		"perl --version",
		"awk --version",
		"sed --version > /tmp/v.txt 2>&1",
		"python3 --help",
		"command ruby --version",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("REFUSED a question: %q — %s", cmd, f.String())
		}
	}
}

// ⭐ And the half that keeps it from being a hole: a version flag is not a
// passport for the rest of the command line.
func TestAVersionFlagDoesNotExcuseTheWork(t *testing.T) {
	for _, cmd := range []string{
		`ruby -e 'puts 1'`,
		`python3 -c "import json;print(1)"`,
		`ruby --version -e 'puts 1'`,
		`python3 --version script.py`,
		`perl -pi -e 's/a/b/' README.md`,
		`awk '{print $1}' /tmp/f.txt`,
		`sed -n '1,5p' /tmp/f.txt`,
		// A pipeline is refused before this ever runs, and must stay refused:
		// the version is not the point, the pipe is.
		`ruby --version | head -1`,
		`python3 --version | grep 3`,
	} {
		if f := Check(cmd); !f.Found() {
			t.Errorf("NOT refused: %q", cmd)
		}
	}
}

// ⭐ And the tightening that comes with the same correction: double quotes do
// not stop a command substitution, so a loop hidden in one is a loop the shell
// really runs. The old helper called this quoted and allowed it.
func TestALoopInsideASubstitutionIsStillALoop(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"a loop in a substitution inside double quotes",
			`echo "$(for f in *.go; do echo "$f"; done)"`},
		{"a loop in a backtick substitution",
			"echo \"`while read -r l; do echo $l; done < /tmp/f`\""},
	} {
		if f := Check(c.cmd); !f.Found() {
			t.Errorf("%s: NOT refused: %q", c.name, c.cmd)
		}
	}
}

// ⭐ The half that keeps the guard from being worked around. This is the
// broadest rule in the repository, so the allow table is the longer one: a
// guard that refuses ordinary work is a guard somebody switches off wholesale.
func TestItAllowsInvokingATool(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"invoking a tool", `go test -count=1 ./...`},
		{"with flags and paths", `go vet ./cmd/... ./internal/...`},
		{"the sanctioned way to process", `go run ./internal/macapp -install`},
		{"a redirect is NOT a pipe, and is what the rule asks for",
			`go test -count=1 ./... > /tmp/out.txt 2>&1`},
		{"both streams redirected", `go build ./... 2>/tmp/err.txt`},
		{"a commit message in a FILE", `git commit -F /tmp/msg.txt`},
		{"a body in a file", `gh pr create --title x --body-file /tmp/body.md`},
		{"git with a reading flag", `git diff --name-only origin/main`},
		{"a tool whose own flag says file", `credscan scan --verbose /tmp`},
		{"gitpush, the wrapper", `gitpush origin main`},
		{"ghmerge", `ghmerge 53`},
		{"agentsync", `agentsync claim --note "x" go-macos/objc`},
		{"writing ABOUT a pipeline in single quotes",
			`echo 'never write a | b and then read $?'`},
		{"writing about a loop in single quotes",
			`echo 'for i in 1 2 3; do echo; done is a program'`},
		{"a quoted heredoc is literal",
			"cat <<'EOF'\nfor i in 1 2 3; do awk '{print}'; done\nEOF"},
		{"grep looking for the word sed", `grep -rn "sed" --include="*.go" .`},
		{"a single grep is invoking a tool", `grep -c require go.mod`},
		{"ls", `ls -la ~/Applications/godl.app`},
		{"an empty command", ``},
		{"a semicolon between two invocations", `go build ./... ; go vet ./...`},
		{"&& between two invocations", `go build ./... && go test ./...`},
	} {
		if f := Check(c.cmd); f.Found() {
			t.Errorf("%s: wrongly refused (%s): %q", c.name, f, c.cmd)
		}
	}
}

// The refusal must say WHAT to do instead, per kind, or it gets worked around.
func TestEachKindCarriesItsOwnRemedy(t *testing.T) {
	for _, c := range []struct{ cmd, want string }{
		{`a | b`, "redirect"},
		{`awk '{print}' f`, "go run"},
		{`for i in 1; do echo; done`, "program"},
		{"git commit -F -", "Write tool"},
	} {
		f := Check(c.cmd)
		if !f.Found() {
			t.Fatalf("%q was not refused", c.cmd)
		}
		if !strings.Contains(f.Instead, c.want) {
			t.Errorf("%q: the remedy does not mention %q: %s", c.cmd, c.want, f.Instead)
		}
	}
}

// ⛔ `grep sed x` searches for a word; it does not run sed. Only the command
// word of a stage decides, or the guard refuses every grep for the rule's own
// text — including the greps in this repository's documentation.
func TestAnInterpreterNamedAsAnARGUMENTIsNotRunning(t *testing.T) {
	for _, cmd := range []string{
		`grep -rn sed --include="*.go" .`,
		`go test -run TestSed ./...`,
		`git log --grep awk`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("an interpreter named as an argument was taken for one running (%s): %q", f, cmd)
		}
	}
}
