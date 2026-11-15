package emptypush

import (
	"strings"
	"testing"
)

// TestAppliesToTheShapeBothOccurrencesHad is the corpus that matters: these are
// the argument lists that actually published empty branches.
func TestAppliesToTheShapeBothOccurrencesHad(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		branch string
	}{
		{"2026-11-01", []string{"-u", "origin", "work"}, "work"},
		{"2026-11-15", []string{"-u", "origin", "docs-generated-split"}, "docs-generated-split"},
		{"remote only", []string{"origin"}, "work"},
		{"no arguments at all", nil, "work"},
		{"force-with-lease is still a plain push", []string{"--force-with-lease", "origin", "work"}, "work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !Applies(tc.args, tc.branch) {
				t.Errorf("Applies(%q, %q) = false, so the rule would not look", tc.args, tc.branch)
			}
		})
	}
}

// TestDeclinesWhatItCannotReasonAbout is the half that keeps this from refusing
// work it does not understand. Each of these publishes something other than, or
// more than, the current branch.
func TestDeclinesWhatItCannotReasonAbout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		branch string
	}{
		{"a tag push", []string{"origin", "--tags"}, "main"},
		{"deleting a remote branch", []string{"origin", "--delete", "old"}, "work"},
		{"deleting, short flag", []string{"origin", "-d", "old"}, "work"},
		{"every branch", []string{"--all", "origin"}, "work"},
		{"a mirror", []string{"--mirror", "origin"}, "work"},
		{"an explicit refspec", []string{"origin", "HEAD:refs/heads/work"}, "work"},
		{"a colon refspec deleting a ref", []string{"origin", ":old"}, "work"},
		{"another branch by name", []string{"origin", "other"}, "work"},
		{"two refspecs", []string{"origin", "work", "other"}, "work"},
		{"a tag by name", []string{"origin", "v0.102.0"}, "main"},
		{"detached HEAD has no branch to publish", []string{"-u", "origin", "work"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if Applies(tc.args, tc.branch) {
				t.Errorf("Applies(%q, %q) = true; it would judge a push it cannot reason about", tc.args, tc.branch)
			}
		})
	}
}

// TestPushingATagNamedLikeTheBranchIsStillJudged documents a deliberate edge:
// `gitpush origin <current-branch>` is judged even though the argument could in
// principle name a tag. If a tag and the current branch share a name, git itself
// is ambiguous and the push is not something to wave through.
func TestPushingATagNamedLikeTheBranchIsStillJudged(t *testing.T) {
	if !Applies([]string{"origin", "work"}, "work") {
		t.Error("a push of the current branch by name is not judged")
	}
}

func TestWhyNamesTheRemoteAndTheConsequence(t *testing.T) {
	w := Why("origin")
	for _, want := range []string{"NO commits", "origin", "none of your work", "success"} {
		if !strings.Contains(w, want) {
			t.Errorf("Why(\"origin\") = %q, does not mention %q", w, want)
		}
	}
}

// TestTheAdviceNamesTheEscapeAndTheHookLesson keeps the refusal actionable: the
// escape must be discoverable from the message, and the hook-refusal trap is the
// mechanism of the second occurrence.
func TestTheAdviceNamesTheEscapeAndTheHookLesson(t *testing.T) {
	for _, want := range []string{Escape, "git log --oneline -1", "WHOLE command"} {
		if !strings.Contains(Advice, want) {
			t.Errorf("Advice does not mention %q: %q", want, Advice)
		}
	}
	if Escape != "GITSAFE_ALLOW_EMPTY_PUSH" {
		t.Errorf("Escape = %q, which is not the documented spelling", Escape)
	}
}
