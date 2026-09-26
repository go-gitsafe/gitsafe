package redact

import (
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/credurl"
)

// TestCleanKnowsMoreThanGitHub.
//
// Clean is what the global pre-push hook and gitpush ask before they let a push
// go. Its list of shapes knew GitHub and nothing else, so this answered "clean",
// measured, before the list became one table:
//
//	Clean("https://glpat-…@gitlab.com/o/r.git") = true
//
// which is a GitLab token in a push URL, pushed without a word. Both halves are
// asserted here: the credential forms must be refused AND the ordinary ssh
// remotes must not be, because a hook that refuses those is a hook somebody
// switches off.
func TestCleanKnowsMoreThanGitHub(t *testing.T) {
	for _, s := range credurl.Shapes() {
		v := s.Prefix + strings.Repeat("Ab3", 8)
		if s.Prefix == "AKIA" || s.Prefix == "ASIA" {
			v = s.Prefix + strings.Repeat("7ABCDE", 4)[:16]
		}
		for _, u := range []string{
			"https://" + v + "@host.test/o/r.git",
			"https://oauth2:" + v + "@host.test/o/r.git",
		} {
			if Clean(u) {
				t.Errorf("%s (%s) in a URL read as clean", s.Name, s.Prefix)
				break
			}
		}
	}
	// An issuer nobody here has heard of: the shapes cannot know it, and the URL
	// still carries a credential. This is the half a prefix list cannot cover.
	if Clean("https://Zq7Kp2Lm9Rt4Wx6Yn1Bv3Cd5Ef8@host.test/o/r.git") {
		t.Error("a high-entropy userinfo from an unknown issuer read as clean")
	}
	for _, u := range []string{
		"https://github.com/o/r.git",
		"ssh://git@github.com/o/r.git",
		"git@plmlab.math.cnrs.fr:team/repo",
		"https://x-access-token@github.com",
		"",
	} {
		if !Clean(u) {
			t.Errorf("refused an ordinary remote %q", u)
		}
	}
}

// TestTheNewPrefixesAreAlsoMasked: refusing an action and masking a transcript
// are two callers of the same table, and a shape that is refused but not masked
// would be reported in full by whatever printed the refusal.
func TestTheNewPrefixesAreAlsoMasked(t *testing.T) {
	for _, prefix := range []string{"glpat-", "gldt-", "glrt-", "ghu_", "ghr_", "xoxa-"} {
		v := prefix + strings.Repeat("Ab3", 8)
		got := New().String("pushing to https://" + v + "@host.test/o/r.git")
		if strings.Contains(got, v) {
			t.Errorf("%s survived redaction: %s", prefix, got)
		}
	}
}
