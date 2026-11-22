package chainedgate

import "testing"

func TestAFindingThatIsNotOneHasNoText(t *testing.T) {
	if s := (Finding{}).String(); s != "" {
		t.Errorf("String of the zero Finding = %q", s)
	}
}

// A word that is not a legal variable name is not an assignment, so it is the
// command and not a prefix to skip.
func TestAWordWithAnIllegalNameIsNotAnAssignment(t *testing.T) {
	if isAssignment("a-b=c") {
		t.Error("a-b=c read as an assignment")
	}
	if got := words("a-b=c prbody"); len(got) != 2 {
		t.Errorf("words = %q, want both words kept", got)
	}
}

// A statement with no words is no gate.
func TestAnEmptyStatementIsNoGate(t *testing.T) {
	if _, ok := gateIn(""); ok {
		t.Error("an empty statement read as a gate")
	}
	if _, ok := gateIn("| |"); ok {
		t.Error("empty stages read as a gate")
	}
}

// `gh` with too few words is no pull request, and an unknown global flag is
// stepped over rather than ending the read.
func TestPRCreateReadsPastUnknownGlobalFlags(t *testing.T) {
	if _, _, ok := prCreateAt([]string{"gh", "pr"}); ok {
		t.Error("`gh pr` read as a creation")
	}
	if _, repo, ok := prCreateAt([]string{"gh", "--paginate", "pr", "create", "--body-file", "b.md"}); !ok || repo != "<owner/repo>" {
		t.Errorf("an unknown global flag stopped the read: ok=%v repo=%q", ok, repo)
	}
}
