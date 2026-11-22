package bgstatus

import (
	"strings"
	"testing"
)

// The two command lines whose notifications said "exit code 0" on 2026-11-22,
// copied from the session that wrote this package. Both tools had exited 1.
const (
	sweep   = `mutsweep -dir . -run-in . -- go test ./... > $L 2>&1; echo "mutsweep rc=$?" >> $L`
	twoStep = `ratewait --rest 400 --timeout 55m > $L 2>&1; echo "ratewait rc=$?" >> $L; prwait -repo o/r 11 >> $L 2>&1; echo "prwait rc=$?" >> $L`
)

// ⛔ The founding case AND its control in one test: the SAME text is a defect in
// the background and correct in the foreground, so a rule that read only the
// text would have to choose one of them wrongly.
func TestTheTwoNotificationsThatSaidZeroAreRefusedOnlyInTheBackground(t *testing.T) {
	for _, cmd := range []string{sweep, twoStep} {
		if f := Check(cmd, true); !f.Found() {
			t.Errorf("allowed a background line whose notification said 0:\n  %s", cmd)
		}
		if f := Check(cmd, false); f.Found() {
			t.Errorf("refused in the FOREGROUND, where the file is read:\n  %s\n  %s", cmd, f)
		}
	}
}

// ⛔ `&&` is the correct spelling, and a rule with no correct form gets routed
// around. A failing first command short-circuits, so the compound's status is
// still that command's.
func TestAndAndKeepsTheFailureAndIsAllowed(t *testing.T) {
	for _, cmd := range []string{
		`go test ./... > $L 2>&1 && echo "rc=$?" >> $L`,
		`brief -n 5 -- a && brief -n 20 -- b`,
		`a > f 2>&1 && b >> f 2>&1 && echo "rc=$?" >> f`,
	} {
		if f := Check(cmd, true); f.Found() {
			t.Errorf("refused the correct spelling:\n  %s\n  %s", cmd, f)
		}
	}
}

func TestTheShapesThisRuleIsNotAbout(t *testing.T) {
	for name, cmd := range map[string]string{
		"nothing discarded":                  `gotest ./... > /Users/x/Documents/log.txt 2>&1`,
		"brief carries it":                   `brief -n 20 -- mutsweep -dir . -- go test ./...`,
		"no status claimed":                  `go build ./... > f 2>&1; echo done >> f`,
		"writing about the form":             `agentsync tell repo 'never write cmd > f; echo "rc=$?" >> f'`,
		"a status with no subject before it": `echo "rc=$?"`,
	} {
		if f := Check(cmd, true); f.Found() {
			t.Errorf("%s: refused\n  %s\n  %s", name, cmd, f)
		}
	}
}

// ⛔ Double quotes EXPAND, so `"rc=$?"` is the mistake itself and not a mention
// of it — and it is the spelling that was actually typed, twice. This is the
// one question [pipestatus.ReadsStatus] is exported to answer once.
func TestDoubleQuotesAreTheFormAndSingleQuotesAreNot(t *testing.T) {
	if f := Check(`a > f 2>&1; echo "rc=$?" >> f`, true); !f.Found() {
		t.Error(`allowed the double-quoted spelling, which is the one that was typed`)
	}
	if f := Check(`a > f 2>&1; echo 'rc=$?' >> f`, true); f.Found() {
		t.Errorf("refused a single-quoted mention, which the shell does not expand: %s", f)
	}
}

func TestANewlineSeparatesStatementsTheSameWay(t *testing.T) {
	f := Check("go test ./... > f 2>&1\necho \"rc=$?\" >> f", true)
	if !f.Found() {
		t.Fatal("a newline between them was read as one statement")
	}
	if f.Sep != "\n" {
		t.Errorf("Sep = %q, want a newline", f.Sep)
	}
	if !strings.Contains(f.String(), "a newline") {
		t.Errorf("the refusal spells the newline as itself: %s", f)
	}
}

// `||` runs the next statement exactly when the first FAILED, which is the case
// the status was wanted for, and the line then exits 0.
func TestOrOrDiscardsTheFailureItWasWrittenToCatch(t *testing.T) {
	f := Check(`go test ./... > f 2>&1 || echo "rc=$?" >> f`, true)
	if !f.Found() {
		t.Fatal("allowed `||`, which reports 0 for exactly the runs that failed")
	}
	if f.Sep != "||" {
		t.Errorf("Sep = %q, want ||", f.Sep)
	}
}

func TestTheRefusalPointsAtBothStatementsAndNotAtItself(t *testing.T) {
	f := Check(sweep, true)
	if !f.Found() {
		t.Fatal("the founding case is not refused")
	}
	if !strings.HasPrefix(f.Statement, "echo ") {
		t.Errorf("Statement = %q, want the echo", f.Statement)
	}
	if !strings.HasPrefix(f.Subject, "mutsweep ") {
		t.Errorf("Subject = %q, want the command whose status it is", f.Subject)
	}
	if f.Sep != ";" {
		t.Errorf("Sep = %q, want ;", f.Sep)
	}
	for _, s := range []string{f.String(), f.Why(), Advice} {
		if s == "" {
			t.Error("a refusal with nothing in it")
		}
	}
	if !strings.Contains(Advice, "brief") {
		t.Error("the advice does not name the program that already does this")
	}
	// ⛔ The zero value is "nothing found", and must not print as a refusal.
	var none Finding
	if none.Found() || none.String() != "" {
		t.Errorf("the zero value reads as a finding: %q", none.String())
	}
}

// An empty statement between the two — `a ;; echo $?` — must not hide the
// subject: the search walks back past it rather than stopping.
func TestAnEmptyStatementBetweenThemDoesNotHideTheSubject(t *testing.T) {
	if f := Check(`a > f 2>&1 ;; echo "rc=$?" >> f`, true); !f.Found() {
		t.Error("an empty statement between them was read as the subject")
	}
}

// A quoted heredoc body is removed before the split, so a commit message or a
// README section that quotes the form is not the form.
func TestAQuotedHeredocBodyIsNotTheForm(t *testing.T) {
	cmd := "cat <<'EOF' > msg.txt\nnever write: a > f; echo \"rc=$?\" >> f\nEOF"
	if f := Check(cmd, true); f.Found() {
		t.Errorf("refused a quoted heredoc that writes ABOUT the form: %s", f)
	}
}
