package shellwork

import (
	"strings"
	"testing"
)

func TestAFindingSpeaksOnlyWhenItIsOne(t *testing.T) {
	if s := (Finding{}).String(); s != "" {
		t.Errorf("String of the zero Finding = %q", s)
	}
	f := Finding{Kind: "a pipeline", Match: "a | b", Instead: "go run"}
	if !strings.Contains(f.Why(), "a pipeline") || !strings.Contains(f.String(), "a | b") {
		t.Errorf("Why/String = %q / %q", f.Why(), f.String())
	}
}

// One argument that is not a version flag is a script, not a question.
func TestOneArgumentThatIsNotAVersionFlagIsNotAQuestion(t *testing.T) {
	if versionQuery([]string{"script.rb"}) {
		t.Error("a script path read as a version query")
	}
}
