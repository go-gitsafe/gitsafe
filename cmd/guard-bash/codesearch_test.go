package main

import (
	"strings"
	"testing"
)

// GitHub code search is refused through the whole hook, with the replacement
// named; the sibling searches that read the tracker are not.
func TestCodeSearchRefusedThroughTheHook(t *testing.T) {
	out, code := drive(t, `{"tool_input":{"command":"gh api search/code -f q=\"A11yElements repo:o/r\""}}`)
	if code != 0 || !strings.Contains(out, `"deny"`) {
		t.Fatalf("code search was not denied (exit %d):\n%s", code, out)
	}
	for _, want := range []string{"code-search index", "git fetch", "git grep -n", "origin/main"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reason does not mention %q:\n%s", want, out)
		}
	}
	// Not the write-refusal tail, which tells a reader about body files.
	if strings.Contains(out, "body") || strings.Contains(out, "by hand") {
		t.Errorf("the code-search refusal carries the write advice:\n%s", out)
	}
	for _, c := range []string{"gh search issues foo", "gh search prs foo", "gh api repos/x/y/contents/README.md", "gh api -X GET search/issues"} {
		if out, _ := drive(t, `{"tool_input":{"command":"`+c+`"}}`); strings.Contains(out, "deny") {
			t.Errorf("%q was refused:\n%s", c, out)
		}
	}
}
