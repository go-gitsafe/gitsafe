package pipestatus

import (
	"strings"
	"testing"
)

func TestAFindingSpeaksOnlyWhenItIsOne(t *testing.T) {
	if s := (Finding{}).String(); s != "" {
		t.Errorf("String of the zero Finding = %q", s)
	}
	f := Finding{Statement: "echo $?", Pipeline: "a | b"}
	if !strings.Contains(f.String(), "a | b") || f.Why() == "" {
		t.Errorf("String/Why = %q / %q", f.String(), f.Why())
	}
}
