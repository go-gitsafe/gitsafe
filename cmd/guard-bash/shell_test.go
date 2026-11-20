package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func commandJSON(t *testing.T, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"cwd": "/Users/me/src/r", "tool_input": map[string]any{"command": cmd}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The grammar for a backtick follows $SHELL (#156), through the whole hook: the
// ordinary traffic that mentions a backtick in a quoted heredoc passes under zsh,
// the same command is refused under bash by name, and a shell with no measured
// grammar refuses.
func TestTheBacktickGrammarFollowsTheShell(t *testing.T) {
	const commit = "git commit -q -m \"$(cat <<'EOF'\nfix: handle `x` here\nEOF\n)\""
	const live = "echo `date`"
	for _, tc := range []struct {
		shell, cmd string
		want       string // "" = allowed, else a substring of the refusal
	}{
		{"/bin/zsh", commit, ""},
		{"/bin/zsh", "echo 'a `b` c'", ""},
		{"/bin/zsh", live, "command substitution"},
		{"/bin/zsh", "echo hi # `x`", "cannot read with certainty"},
		{"/bin/bash", commit, "cannot read with certainty"},
		{"/bin/bash", "echo 'a `b` c'", ""},
		{"/bin/fish", "echo 'a `b` c'", "fish"},
		{"", "echo 'a `b` c'", "unset"},
		{"/bin/fish", "echo hi", ""},
	} {
		t.Setenv("SHELL", tc.shell)
		out, _ := drive(t, commandJSON(t, tc.cmd))
		if tc.want == "" {
			if out != "" {
				t.Errorf("SHELL=%q refused %q:\n%s", tc.shell, tc.cmd, out)
			}
			continue
		}
		if !strings.Contains(out, `"deny"`) || !strings.Contains(out, tc.want) {
			t.Errorf("SHELL=%q, %q: want a refusal naming %q, got:\n%s", tc.shell, tc.cmd, tc.want, out)
		}
	}
}
