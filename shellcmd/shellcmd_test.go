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

// TestInsideQuotes asks about the offset of a marker, so each case reads as
// the sentence it is: the marker is `WORD`, and the question is whether the
// shell would interpret something written there.
func TestInsideQuotes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cmd   string
		quote bool
	}{
		{"bare", "echo WORD", false},
		{"in single quotes", "echo 'a WORD b'", true},
		{"in double quotes", `echo "a WORD b"`, true},
		// ⛔ The measured false positive: a message that TALKS about a loop.
		{"an English sentence in double quotes", `agentsync tell repo "green WORD tests run"`, true},
		{"after a closed single-quoted string", "echo 'done' WORD", false},
		{"after a closed double-quoted string", `echo "done" WORD`, false},
		{"between two quoted strings", `echo "a" WORD "b"`, false},
		// ⛔ Double quotes do NOT stop a substitution: the shell runs this.
		{"in a substitution inside double quotes", `echo "$(find . -name WORD)"`, false},
		{"after the substitution closes, still quoted", `echo "$(ls) WORD"`, true},
		{"in a backtick substitution inside double quotes", "echo \"`grep WORD f`\"", false},
		{"in a substitution in single quotes is literal", `echo '$(find WORD)'`, true},
		{"nested substitutions", `echo "$(echo "$(ls WORD)")"`, false},
		{"an escaped quote does not end the quoting", `echo "a \" WORD b"`, true},
		// ⛔ Unsure means refuse: an unbalanced quote is a broken command.
		{"an unbalanced single quote", "echo 'a WORD b", false},
		{"an unbalanced double quote", `echo "a WORD b`, false},
		{"an unclosed substitution", `echo "$(ls WORD"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			off := strings.Index(tc.cmd, "WORD")
			if off < 0 {
				t.Fatalf("the case has no marker in it: %q", tc.cmd)
			}
			if got := InsideQuotes(tc.cmd, off); got != tc.quote {
				t.Errorf("InsideQuotes(%q, %d) = %v, want %v", tc.cmd, off, got, tc.quote)
			}
		})
	}
}

// An offset nobody can point at is not inside anything.
func TestInsideQuotesOutOfRange(t *testing.T) {
	for _, off := range []int{-1, 0, 5, 1000} {
		if InsideQuotes("", off) {
			t.Errorf("InsideQuotes(%q, %d) = true", "", off)
		}
	}
	if InsideQuotes("'abc'", 5) {
		t.Error("an offset one past the end reads as quoted")
	}
}

// ⛔ Stages has always known both quote characters; this says so out loud,
// because the loop rule once disagreed with it about the same sentence.
func TestStagesAndInsideQuotesAgreeAboutDoubleQuotes(t *testing.T) {
	const cmd = `agentsync tell repo "redirect rather than | head"`
	if n := len(Stages(cmd)); n != 1 {
		t.Errorf("Stages found %d stages: a pipe inside double quotes is an argument", n)
	}
	if !InsideQuotes(cmd, strings.Index(cmd, "|")) {
		t.Error("InsideQuotes disagrees with Stages about the same pipe")
	}
}
