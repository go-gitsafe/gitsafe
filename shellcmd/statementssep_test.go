package shellcmd

import (
	"strings"
	"testing"
)

var sepCases = []string{
	`a; b`,
	`a && b`,
	`a || b`,
	`a | b`,
	"a\nb",
	`a";"b`,
	`echo 'a && b'`,
	`a > f 2>&1; echo "rc=$?" >> f`,
	`a ;; b`,
	``,
	`;`,
	`&`,
	`a &`,
	`a &&`,
	`a ||`,
}

// ⛔ ONE NOTION OF "STATEMENT", NOT TWO: Statements must be exactly the
// statements StatementsSep reports, because bgstatus asks the second for the
// separator and pipestatus asks the first for the text, about the same line.
func TestStatementsIsStatementsSepsFirstHalf(t *testing.T) {
	for _, cmd := range sepCases {
		stmts, seps := StatementsSep(cmd)
		if len(stmts) != len(seps) {
			t.Fatalf("%q: %d statement(s) and %d separator(s)", cmd, len(stmts), len(seps))
		}
		if seps[len(seps)-1] != "" {
			t.Errorf("%q: the last statement is ended by %q, not by nothing", cmd, seps[len(seps)-1])
		}
		got := Statements(cmd)
		if strings.Join(got, "\x00") != strings.Join(stmts, "\x00") {
			t.Errorf("%q: Statements %q differs from StatementsSep %q", cmd, got, stmts)
		}
	}
}

// The separators are the ones the shell uses to decide whose status the compound
// carries, so each has to be reported as itself and not folded into another.
func TestEachSeparatorIsReportedAsItself(t *testing.T) {
	for cmd, want := range map[string]string{
		`a; b`:          ";",
		`a && b`:        "&&",
		`a || b`:        "||",
		"a\nb":          "\n",
		`a | b`:         "", // a pipeline is ONE statement
		`a";"b`:         "", // quoted: not a separator
		`a &`:           "", // a bare & is not a separator here
		`echo 'a && b'`: "",
	} {
		_, seps := StatementsSep(cmd)
		if seps[0] != want {
			t.Errorf("%q: first separator %q, want %q", cmd, seps[0], want)
		}
	}
}

// ⛔ The round trip is the exact statement that the split loses nothing: the
// statements and their separators, concatenated in order, ARE the command. A
// split that dropped a byte would let a rule read a line that was never typed.
func FuzzStatementsSepRoundTrip(f *testing.F) {
	for _, c := range sepCases {
		f.Add(c)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		stmts, seps := StatementsSep(cmd)
		if len(stmts) != len(seps) {
			t.Fatalf("%d statement(s), %d separator(s)", len(stmts), len(seps))
		}
		var b strings.Builder
		for i := range stmts {
			b.WriteString(stmts[i])
			b.WriteString(seps[i])
		}
		if b.String() != cmd {
			t.Fatalf("round trip lost bytes:\n in  %q\n out %q\n stmts %q\n seps %q", cmd, b.String(), stmts, seps)
		}
	})
}
