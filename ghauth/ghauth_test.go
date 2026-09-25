package ghauth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnEmptyTokenFile(t *testing.T) {
	// An empty file is not a token, and saying so beats asking GitHub who
	// nobody is.
	p := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(p, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(p); err == nil {
		t.Error("an empty file was accepted as a token")
	}
}

func TestATokenIsReadWithoutItsWhitespace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("  abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc" {
		t.Errorf("read %q", got)
	}
}

// GitHub's scopes are a HIERARCHY, not a list. A token ticked `write:packages`
// can read packages, and its X-OAuth-Scopes header says only `write:packages`.
//
// This is the reading that cost a wrong diagnosis: a token carrying
// `delete:packages, write:packages` was reported as missing `read:packages`,
// the reading was believed, and the API call that failed was blamed on the
// scope rather than on the token it actually used. A guard that cries wolf
// gets read as a fact.
func TestScopesAreAHierarchy(t *testing.T) {
	for _, tc := range []struct {
		name string
		have []string
		want []string
		gone []string // what must still be reported missing
	}{
		{"write:packages covers read", []string{"delete:packages", "write:packages"}, []string{"read:packages"}, nil},
		{"delete does not come free", []string{"write:packages"}, []string{"delete:packages"}, []string{"delete:packages"}},
		{"admin:org covers read:org", []string{"admin:org"}, []string{"read:org", "write:org"}, nil},
		{"repo covers public_repo", []string{"repo"}, []string{"public_repo", "repo:status"}, nil},
		{"user covers user:email", []string{"user"}, []string{"user:email"}, nil},
		{"a scope nothing contains", []string{"gist"}, []string{"gist"}, nil},
		{"and one that is simply absent", []string{"gist"}, []string{"workflow"}, []string{"workflow"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Missing(tc.have, tc.want)
			if len(got) != len(tc.gone) {
				t.Fatalf("Missing(%v, %v) = %v, want %v", tc.have, tc.want, got, tc.gone)
			}
			for i := range got {
				if got[i] != tc.gone[i] {
					t.Errorf("missingFrom = %v, want %v", got, tc.gone)
				}
			}
		})
	}
}

// Expansion is transitive and terminates: admin:public_key contains
// write:public_key, which contains read:public_key.
func TestScopeExpansionIsTransitive(t *testing.T) {
	set := Expand([]string{"admin:public_key"})
	for _, s := range []string{"admin:public_key", "write:public_key", "read:public_key"} {
		if !set[s] {
			t.Errorf("%s is not in the expansion: %v", s, set)
		}
	}
}
