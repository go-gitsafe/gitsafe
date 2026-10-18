package pipestatus

import "testing"

// ⛔ The first three cases are the forms REALLY typed on 2026-10-11, in the same
// afternoon, by somebody who had re-read their own note between two of them.
func TestItRefusesAStatusThatBelongsToTheLastStage(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"after go build | tail, exactly as typed",
			`go build -o /dev/null . | tail -5 ; echo "status=$?"`},
		{"after go build | head, over two lines",
			"GOWORK=off go build ./cmd/reauthor/ 2>&1 | head -20 | sed 's/^/  /'\necho \"  build status=$?\""},
		{"after a sweep | tail",
			"reauthor -scan ~/src 2>&1 | tail -40\necho \"status=$?\""},
		{"assigned to a variable", `a | b; code=$?`},
		{"returned as the exit status", `a | b; exit $?`},
		{"tested in a condition", `a | b; if [ $? -ne 0 ]; then echo no; fi`},
		{"the braced spelling", `a | b; echo ${?}`},
		{"the pipeline is two statements up, with blank space between",
			"a | b;\n\necho $?"},
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
		{"no pipeline: the status really is the command's", `go build ./...; echo $?`},
		{"pipefail: the status IS the pipeline's", `set -o pipefail; a | b; echo $?`},
		{"pipefail with other flags", `set -eo pipefail
a | b
echo $?`},
		{"nobody reads a status", `go build ./... | tail -5`},
		{"the status is USED, not reported", `a | b && echo ok`},
		{"|| is not a pipeline", `a || b; echo $?`},
		// ⭐ A pipeline EARLIER in the chain does not poison a later statement:
		// here $? is the status of `c`, which is not a pipeline, so the read is
		// correct. (This case was on the wrong side of the table: the code was
		// right and the test was wrong.)
		{"a pipeline further up, but the status is c's", `a | b && c; echo $?`},
		{"|| followed by a read", `make || true; echo $?`},
		{"writing about it in single quotes", `echo 'do not write a | b then $?'`},
		{"writing about it in a quoted heredoc", `cat > note.md <<'EOF'
a | b ; echo "status=$?"
EOF`},
		{"a pipe inside a string is not a pipe", `grep "a|b" f.txt; echo $?`},
		{"the empty command", ``},
		{"$? alone, with nothing before it", `echo $?`},
	} {
		if f := Check(c.cmd); f.Found() {
			t.Errorf("%s: wrongly refused (%s): %q", c.name, f, c.cmd)
		}
	}
}

// ⛔ DOUBLE quotes expand, so `echo "status=$?"` is the mistake itself — and
// that is precisely the spelling that was typed three times.
func TestDoubleQuotesAreNotAnExcuse(t *testing.T) {
	if f := Check(`a | b; echo "status=$?"`); !f.Found() {
		t.Error(`"status=$?" in double quotes expands: that is not writing about it, that is doing it`)
	}
	if f := Check(`a | b; echo 'status=$?'`); f.Found() {
		t.Errorf("inside single quotes the shell does not expand: %s", f)
	}
}

// The refusal must NAME both statements, so it points at something rather than
// asserting.
func TestTheRefusalNamesBothSides(t *testing.T) {
	f := Check(`go build ./... | tail -5; echo "status=$?"`)
	if !f.Found() {
		t.Fatal("not refused")
	}
	if f.Pipeline == "" || f.Statement == "" {
		t.Errorf("the refusal does not name both sides: %+v", f)
	}
	for _, want := range []string{"tail -5", "LAST stage"} {
		if !contains(f.String(), want) {
			t.Errorf("the explanation does not say %q: %s", want, f)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
