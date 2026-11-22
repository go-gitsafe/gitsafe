package literaltick

import (
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

func TestAFindingThatIsNotOneHasNoText(t *testing.T) {
	if s := (Finding{}).String(); s != "" {
		t.Errorf("String of the zero Finding = %q", s)
	}
}

// When the grammar refuses but the older reading finds every backtick literal,
// the grammar's word stands: refuse at the first backtick.
func TestTheGrammarsRefusalStandsWhenTheOlderReadingSeesNothing(t *testing.T) {
	const cmd = "echo `x`"
	cls := make([]byte, len(cmd))
	for i := range cls {
		cls[i] = shellcmd.ClassSingle
	}
	f := refusedAt(cmd, cls, 5)
	if !f.Found() || f.Off != 5 {
		t.Errorf("refusedAt = %+v, want a refusal at 5", f)
	}
	if f.InDoubleQuotes {
		t.Error("a single-quoted class reported as double-quoted")
	}
}

// Why says which of the three things happened.
func TestWhyNamesWhatHappened(t *testing.T) {
	dq := Finding{Context: "x", InDoubleQuotes: true}.Why()
	plainWhy := Finding{Context: "x"}.Why()
	unsure := Finding{Context: "x", Construct: "an unquoted #"}.Why()
	if !strings.Contains(dq, "double quotes") || strings.Contains(plainWhy, "double quotes") {
		t.Errorf("double-quote reason: %q / %q", dq, plainWhy)
	}
	if !strings.Contains(unsure, "an unquoted #") {
		t.Errorf("uncertain reason: %q", unsure)
	}
}

// A backtick the grammar says is evaluated is reported as that, with where it
// sits, and not as "a construct this guard cannot read".
func TestAnEvaluatedBacktickIsReportedAsEvaluated(t *testing.T) {
	for _, sh := range []string{"zsh", "bash"} {
		f := CheckFor(sh, "echo \"`x`\"")
		if !f.Found() || f.Construct != "" || !f.InDoubleQuotes {
			t.Errorf("%s: CheckFor = %+v", sh, f)
		}
	}
}

// The escape covers the evaluated backtick of a refused command; a backtick that
// is only quoted text elsewhere in it does not have to carry the escape too.
func TestTheEscapeCoversOnlyTheEvaluatedBackticks(t *testing.T) {
	cmd := Escape + "=1 echo `x`; echo '`'"
	if f := CheckFor("zsh", cmd); f.Found() {
		t.Errorf("CheckFor refused %+v", f)
	}
}

// A command with no backtick is never refused, whatever shell it names.
func TestNoBacktickIsNeverRefused(t *testing.T) {
	for _, sh := range []string{"zsh", "bash", "fish", "", "/bin/zsh"} {
		if f := CheckFor(sh, "echo hi"); f.Found() {
			t.Errorf("%q: %+v", sh, f)
		}
	}
}
