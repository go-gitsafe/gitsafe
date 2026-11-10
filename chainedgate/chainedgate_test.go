package chainedgate

import "testing"

// TestTheThreeCommandLinesThatCausedThis are the real ones, verbatim in shape.
//
// Each ran `prbody`, saw it refuse, and created the pull request or issue
// anyway, because both were in one Bash call.
func TestTheThreeCommandLinesThatCausedThis(t *testing.T) {
	for _, cmd := range []string{
		`prbody "$S/zt-pr.md" --repo go-zpkg/zbk > "$S/out.txt" 2>&1; echo "body=$?"; gh pr create --repo go-zpkg/zbk --base main --head b --title "t" --body-file "$S/zt-pr.md"`,
		`prbody "$S/zx-commit.txt" --repo go-zpkg/zpkgx > "$S/o.txt" 2>&1; gh pr create --repo go-zpkg/zpkgx --base main --head b --title "t" --body-file "$S/zx-commit.txt"`,
		`prbody "$S/i-nextest.md" --repo go-zpkg/packages > "$S/o.txt" 2>&1; ghissue go-zpkg/packages --new --title "t" --body-file "$S/i-nextest.md"`,
	} {
		f := Check(cmd)
		if !f.Found() {
			t.Errorf("not refused:\n%s", cmd)
			continue
		}
		if f.Gate != "prbody" {
			t.Errorf("gate = %q, want prbody", f.Gate)
		}
	}
}

// TestAndIsTheCorrectForm. ⛔ If this ever fails, the rule has no correct
// spelling left and becomes something to route around rather than follow.
func TestAndIsTheCorrectForm(t *testing.T) {
	for _, cmd := range []string{
		`prbody b.md --repo o/r && gh pr create --body-file b.md`,
		`prbody b.md --repo o/r && ghissue o/r --new --title t --body-file b.md`,
		`cd /some/where && prbody b.md --repo o/r && gh pr create --body-file b.md`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("refused the form that DOES enforce the verdict: %s\n%s", f.Why(), cmd)
		}
	}
}

// TestOneSemicolonAnywhereIsEnough: `gate; echo $?; write` reads as more
// careful than `gate; write` and is exactly as broken.
func TestOneSemicolonAnywhereIsEnough(t *testing.T) {
	for _, cmd := range []string{
		`prbody b.md --repo o/r; echo "$?"; gh pr create --body-file b.md`,
		`prbody b.md --repo o/r && echo ok; gh pr create --body-file b.md`,
		`prbody b.md --repo o/r || true && gh pr create --body-file b.md`,
	} {
		if f := Check(cmd); !f.Found() {
			t.Errorf("not refused, although the write runs on a refusal:\n%s", cmd)
		}
	}
}

// TestEachAloneIsFine — the check on its own is how it is normally run, and
// the write on its own is a separate call after reading the verdict.
func TestEachAloneIsFine(t *testing.T) {
	for _, cmd := range []string{
		`prbody b.md --repo o/r`,
		`prbody b.md --repo o/r > out.txt 2>&1`,
		`gh pr create --repo o/r --base main --head b --title t --body-file b.md`,
		`ghissue o/r --new --title t --body-file b.md`,
		`cd /x && prbody b.md --repo o/r`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("refused a command that is only one half: %s\n%s", f.Why(), cmd)
		}
	}
}

// TestTheOrderMatters: a write BEFORE a check is not this defect. It may be
// other things, but the gate cannot have been meant to decide something that
// already happened.
func TestTheOrderMatters(t *testing.T) {
	cmd := `gh pr create --body-file b.md; prbody b.md --repo o/r`
	if f := Check(cmd); f.Found() {
		t.Errorf("refused a write that came FIRST: %s", f.Why())
	}
}

// TestReadsAreNotWrites. ⛔ The table names `gh pr create` and not `gh pr`,
// because looking something up next to a check is the ordinary way to work.
func TestReadsAreNotWrites(t *testing.T) {
	for _, cmd := range []string{
		`prbody b.md --repo o/r; gh pr view 443 --repo o/r`,
		`prbody b.md --repo o/r; gh pr list --repo o/r`,
		`prbody b.md --repo o/r; gh run list --repo o/r`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("refused a READ after the check: %s\n%s", f.Why(), cmd)
		}
	}
}

// TestWritingAboutItIsNotDoingIt: a quoted heredoc is text, not a command.
func TestWritingAboutItIsNotDoingIt(t *testing.T) {
	cmd := "cat > note.md <<'EOF'\nprbody b.md --repo o/r; gh pr create --body-file b.md\nEOF"
	if f := Check(cmd); f.Found() {
		t.Errorf("refused a heredoc that QUOTES the form: %s", f.Why())
	}
}

// TestAPathToTheToolIsStillTheTool.
func TestAPathToTheToolIsStillTheTool(t *testing.T) {
	cmd := `~/.local/bin/prbody b.md --repo o/r; gh pr create --body-file b.md`
	if !Check(cmd).Found() {
		t.Error("a gate invoked by path was not recognised")
	}
}

// TestAPipelineStageCountsAsTheGate, because `prbody b.md | tee log` is still
// the gate being run.
func TestAPipelineStageCountsAsTheGate(t *testing.T) {
	cmd := `prbody b.md --repo o/r | tee log.txt; gh pr create --body-file b.md`
	if !Check(cmd).Found() {
		t.Error("a gate inside a pipeline was not recognised")
	}
}

// TestLeadingAssignmentsAreSkipped, the way a shell reads them.
func TestLeadingAssignmentsAreSkipped(t *testing.T) {
	cmd := `GH_TOKEN=x prbody b.md --repo o/r; GH_TOKEN=x gh pr create --body-file b.md`
	if !Check(cmd).Found() {
		t.Error("VAR=value in front of either half hid it")
	}
}

// TestTheRefusalPointsRatherThanAsserts: a reader must see WHICH two commands.
func TestTheRefusalPointsRatherThanAsserts(t *testing.T) {
	f := Check(`prbody b.md --repo o/r; gh pr create --body-file b.md`)
	if !f.Found() {
		t.Fatal("not refused")
	}
	for _, want := range []string{"prbody", "gh pr create"} {
		if !contains(f.Why()+f.String()+f.Instead, want) {
			t.Errorf("the refusal never names %q:\n%s\n%s\n%s", want, f.Why(), f.String(), f.Instead)
		}
	}
	if !contains(Advice, "&&") {
		t.Error("the advice does not give the correct spelling")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
