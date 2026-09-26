package secretarg

import (
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/credurl"
)

// TestEveryIssuerInTheTableIsRefusedOnACommandLine.
//
// The prefixes used to be written out here, and the list had no ghu_, no ghr_
// and no GitLab at all. Measured before this test existed:
//
//	git clone https://glpat-…@gitlab.com/o/r      → allowed through
//	git remote set-url origin https://ghu_…@…     → allowed through
//
// A machine here works with a GitLab (plmlab.math.cnrs.fr) daily, so that was
// not a theoretical half of the list. The prefixes now come from
// [credurl.Shapes], which is also what judges a remote URL, so a new issuer is
// added in one place and every guard learns it at once.
func TestEveryIssuerInTheTableIsRefusedOnACommandLine(t *testing.T) {
	for _, s := range credurl.Shapes() {
		v := s.Prefix + strings.Repeat("Ab3", 8)
		if s.Prefix == "AKIA" || s.Prefix == "ASIA" {
			v = s.Prefix + strings.Repeat("7ABCDE", 4)[:16]
		}
		for _, cmd := range []string{
			"git clone https://" + v + "@example.test/o/r.git",
			"git remote set-url origin https://oauth2:" + v + "@example.test/o/r.git",
			"curl -H 'Authorization: Bearer " + v + "' https://example.test",
		} {
			if f := Check(cmd); !f.Found() {
				t.Errorf("%s (%s) was allowed onto a command line", s.Name, s.Prefix)
				break
			}
		}
	}
}

// TestTheOrdinaryFormsStayAllowed. This is the half that keeps a guard from
// being worked around: it refuses a command a person then reruns another way,
// and after that it protects nothing. A clone over ssh, and a clone of a public
// repository, are what people type all day.
func TestTheOrdinaryFormsStayAllowed(t *testing.T) {
	for _, cmd := range []string{
		"git clone https://github.com/go-gitsafe/gitsafe.git",
		"git clone ssh://git@github.com/go-gitsafe/gitsafe.git",
		"git clone git@plmlab.math.cnrs.fr:team/repo",
		"git remote set-url origin https://github.com/o/r.git",
		"gitpush origin main",
		"ghscopes workflow",
		"wc -c < ~/.github-token",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("refused an ordinary command %q: %s", cmd, f.Why)
		}
	}
}
