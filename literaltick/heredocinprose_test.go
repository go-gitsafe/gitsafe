package literaltick

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ A HEREDOC IN THE PROSE IS NOT A HEREDOC, and this is the case that decided
// a design question.
//
// The scan bash uses to find the ) that closes a $( … ) has no notion of
// heredocs: it looks at characters. So a PR body that TALKS about a heredoc —
// which this repository's bodies do, this pull request's included — carries a )
// that bash reads as the end of the substitution, and everything after it runs.
//
// This is why unbalancedClose must not be derived from [shellcmd.Classify],
// the obvious-looking simplification. Classify models bash's PARSER: what a
// byte means once the command is parsed, heredocs and all. It would read the
// example below as a real heredoc, class the ) as a body byte, and allow what
// bash runs. The two routines answer different questions, one before parsing
// and one after, and they differ on at least three axes already witnessed here:
// the comment boundary, heredoc awareness, and escapes inside spans.
func TestAHeredocInTheProseIsNotAHeredoc(t *testing.T) {
	var shells []string
	for _, s := range []string{"zsh", "bash", "dash"} {
		if p, err := exec.LookPath(s); err == nil {
			shells = append(shells, p)
		}
	}
	if len(shells) == 0 {
		t.Skip("no shell to ask")
	}

	body := func(line string) string {
		return "echo --body \"$(cat <<'EOF'\n" + line + "\nEOF\n)\"\n"
	}
	cases := []struct{ name, cmd string }{
		{"a quoted heredoc example in the prose", body("cat <<'E'\n) " + T + "\nE")},
		{"an unquoted heredoc example in the prose", body("cat <<E\n) " + T + "\nE")},
		{"prose about the <<- form", body("cat <<-'E'\n) " + T + "\nE")},
	}

	ran := 0
	for _, tc := range cases {
		var ranOn []string
		for _, sh := range shells {
			if runs(t, sh, tc.cmd) {
				ranOn = append(ranOn, filepath.Base(sh))
				ran++
			}
		}
		if len(ranOn) > 0 && !Check(tc.cmd).Found() {
			t.Errorf("FALSE ALLOW -- %s: %s evaluated the backtick and the rule allowed it\n  %s",
				tc.name, strings.Join(ranOn, ", "), tc.cmd)
		}
	}
	if ran == 0 {
		t.Skip("no shell here ends a substitution on these: not judgeable on this platform")
	}
}
