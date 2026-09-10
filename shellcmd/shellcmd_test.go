package shellcmd

import (
	"strings"
	"testing"
)

// A quoted heredoc is literal, so text that QUOTES a forbidden form is not the
// forbidden form. Documentation, a commit message and a note all need to say
// what not to do — including in this repository's own history.
func TestAQuotedHeredocBodyIsRemoved(t *testing.T) {
	for _, tag := range []string{"<<'EOF'", `<<"EOF"`, `<<\EOF`, "<<-'EOF'"} {
		cmd := "cat " + tag + "\nthe forbidden line\nEOF\nafter"
		got := StripQuotedHeredocs(cmd)
		if strings.Contains(got, "the forbidden line") {
			t.Errorf("%s: the body survived: %q", tag, got)
		}
		if !strings.Contains(got, "after") {
			t.Errorf("%s: what followed the heredoc was lost: %q", tag, got)
		}
		if !strings.Contains(got, "cat ") {
			t.Errorf("%s: the opening line was lost, and it may carry a substitution of its own: %q", tag, got)
		}
	}
}

// An UNQUOTED heredoc does expand, so its body is what actually runs and must be
// left in place to be scanned.
func TestAnUnquotedHeredocBodyIsKept(t *testing.T) {
	got := StripQuotedHeredocs("cat <<EOF\nthe expanded line\nEOF")
	if !strings.Contains(got, "the expanded line") {
		t.Errorf("an unquoted body was removed: %q", got)
	}
}

func TestTextWithNoHeredocIsUntouched(t *testing.T) {
	const cmd = "git status --porcelain && echo done"
	if got := StripQuotedHeredocs(cmd); got != cmd {
		t.Errorf("StripQuotedHeredocs changed a plain command: %q", got)
	}
}
