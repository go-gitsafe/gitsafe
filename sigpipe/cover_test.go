package sigpipe

import "testing"

func TestTheZeroFindingSaysNothing(t *testing.T) {
	if s := (Finding{}).String(); s != "" {
		t.Errorf("String of the zero Finding = %q", s)
	}
	if (Finding{}).Why() == "" {
		t.Error("Why is empty")
	}
}

func TestHelpersGiveUpOnEmptyInput(t *testing.T) {
	if quoted("'") || quoted("") {
		t.Error("a stage shorter than two bytes read as quoted")
	}
	if stopsEarly("") || stopsEarly("FOO=1") {
		t.Error("an empty stage can stop early")
	}
	if abandonsWork("") || abandonsWork("FOO=1") {
		t.Error("an empty stage abandons work")
	}
	if isDigits("") || isDuration("") || isDuration("s") {
		t.Error("an empty number read as a number")
	}
	if isDigits("1x") || isDuration("1x") || isDuration("1.2.3") {
		t.Error("a malformed number read as a number")
	}
	if !isDigits("12") || !isDuration("1.5h") {
		t.Error("a well-formed number was refused")
	}
}

// A command whose subcommand never appears, only flags, abandons nothing.
func TestAProducerWithOnlyFlagsAbandonsNothing(t *testing.T) {
	if abandonsWork("go -v") {
		t.Error("`go -v` read as a mutating build")
	}
	if got := Check("go -v | head -5"); got.Found() {
		t.Errorf("Check refused %v", got)
	}
}

// Wrappers with nothing after them leave nothing to read.
func TestWrappersWithNothingAfterThemLeaveNothing(t *testing.T) {
	for _, s := range [][]string{{"timeout", "-k", "5", "10"}, {"sudo", "-n"}, {"env"}} {
		if got := skipPrefix(s); len(got) != 0 {
			t.Errorf("skipPrefix(%q) = %q", s, got)
		}
	}
}
