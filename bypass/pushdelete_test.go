package bypass

import "testing"

// A push that deletes a remote branch names ghbranch, which checks the branch
// first; an ordinary push still names gitpush.
func TestAPushThatDeletesABranchNamesGhbranch(t *testing.T) {
	for _, cmd := range []string{
		"git push origin --delete topic",
		"git push --delete origin topic",
		"git push -d origin topic",
		"git push -fd origin topic",
		"git push origin :topic",
		"git push origin +:topic",
		"cd /x && git push origin :ab/none :ab/memo",
	} {
		f := Check(cmd)
		if !f.Found() || f.Rule != "push" {
			t.Errorf("Check(%q) = %+v, want a push refusal", cmd, f)
			continue
		}
		if f.Match != "git push --delete" || !contains(f.Advice, "ghbranch owner/repo delete") {
			t.Errorf("Check(%q): Match=%q, Advice must name ghbranch:\n%s", cmd, f.Match, f.Advice)
		}
	}
}

func TestAnOrdinaryPushStillNamesGitpush(t *testing.T) {
	for _, cmd := range []string{
		"git push origin main",
		"git push -u origin topic",
		"git push --force-with-lease origin topic",
		"git push --dry-run origin topic",
		"git push origin topic:topic",
		"git push --push-option=d origin topic",
	} {
		f := Check(cmd)
		if !f.Found() || f.Rule != "push" || f.Match != "git push" {
			t.Errorf("Check(%q) = %+v, want the gitpush refusal", cmd, f)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
