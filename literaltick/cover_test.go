package literaltick

import (
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
