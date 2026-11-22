package sshweak

import "testing"

// An export that names something else is allowed.
func TestAnExportOfSomethingElseIsAllowed(t *testing.T) {
	if f := Check("export FOO=1 BAR=2"); f.Found() {
		t.Errorf("Check refused %v", f)
	}
}

// Bundled flags that take no argument, with no -A among them, forward nothing.
func TestBundledFlagsWithoutAForwardNothing(t *testing.T) {
	if forwardsAgent("-qv") {
		t.Error("-qv read as forwarding the agent")
	}
	if !forwardsAgent("-qA") {
		t.Error("-qA not read as forwarding the agent")
	}
}

func TestIdentifiersMayHoldDigitsAfterTheFirstByte(t *testing.T) {
	if !isIdent("A1_b") {
		t.Error("A1_b is an identifier")
	}
	if isIdent("1A") {
		t.Error("1A is not an identifier")
	}
}
