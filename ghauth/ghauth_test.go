package ghauth

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// ⛔⛔ A 403 IS FOUR DIFFERENT SITUATIONS AND ONLY ONE OF THEM IS A DEAD TOKEN.
// ghscopes and ghpkg both said "the token is probably expired or revoked" for
// every non-success, and that sentence sends somebody to revoke and reissue a
// credential that was working.
//
// ⭐ IT HAPPENED. After seven pull requests merged in quick succession, ghmerge
// and then ghscopes both answered 403 while `gitpush --dry-run` on the same
// token answered "Everything up-to-date" -- git authenticating with it one
// second, the API refusing it the next. GitHub's burst limit. The measurement
// said the credential was alive, the label said it was dead, and the label won.
func TestWhyRefusedNamesWhichRefusalItWas(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name   string
		status int
		header http.Header
		body   string
		// want is a phrase the sentence must contain, and notWant one it must
		// NOT: naming the burst limit is only useful if it stops claiming the
		// credential is gone.
		want, notWant string
	}{
		{
			name:   "the burst limit, which GitHub names in the body",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   `{"message":"You have exceeded a secondary rate limit."}`,
			want:   "burst limit", notWant: "expired",
		},
		{
			name:   "the burst limit under its older name",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   `{"message":"abuse detection mechanism triggered"}`,
			want:   "burst limit", notWant: "expired",
		},
		{
			// ⭐ AND THE WAIT IS GITHUB'S OWN NUMBER, so a person does not guess.
			name:   "the burst limit with a wait attached",
			status: http.StatusForbidden,
			header: http.Header{"Retry-After": []string{"47"}},
			body:   `{"message":"slow down"}`,
			want:   "47", notWant: "expired",
		},
		{
			name:   "a Retry-After with nothing else, which is still the burst",
			status: http.StatusTooManyRequests,
			header: http.Header{"Retry-After": []string{"60"}},
			body:   `{}`,
			want:   "burst limit", notWant: "expired",
		},
		{
			name:   "the hourly budget, spent",
			status: http.StatusForbidden,
			header: http.Header{"X-Ratelimit-Remaining": []string{"0"},
				"X-Ratelimit-Reset": []string{"1760000000"}},
			body: `{"message":"API rate limit exceeded"}`,
			want: "hourly budget", notWant: "expired",
		},
		{
			// ⛔ THE ONE CASE THE OLD SENTENCE WAS RIGHT ABOUT, which has to keep
			// saying so or this change trades one wrong diagnosis for another.
			name:   "a credential GitHub will not accept",
			status: http.StatusUnauthorized,
			header: http.Header{},
			body:   `{"message":"Bad credentials"}`,
			want:   "expired", notWant: "burst",
		},
		{
			name:   "bad credentials behind a 403",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   `{"message":"Bad credentials"}`,
			want:   "expired", notWant: "burst",
		},
		{
			// Anything else quotes GitHub rather than inventing a cause.
			name:   "a refusal with its own reason",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   `{"message":"Resource not accessible by personal access token"}`,
			want:   "not accessible by personal access token", notWant: "expired",
		},
		{
			name:   "a refusal with nothing to say",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   ``,
			want:   "403", notWant: "expired",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := WhyRefused(c.status, c.header, []byte(c.body))
			if err == nil {
				t.Fatal("WhyRefused said nothing about a refusal")
			}
			got := err.Error()
			if !strings.Contains(got, c.want) {
				t.Errorf("WhyRefused said %q, want it to mention %q", got, c.want)
			}
			if c.notWant != "" && strings.Contains(got, c.notWant) {
				t.Errorf("WhyRefused said %q, which still blames %q", got, c.notWant)
			}
			// ⛔ AND NO ANSWER MAY QUOTE AN AUTHORIZATION HEADER, however a caller
			// builds one. The inputs here are a status, response headers and
			// GitHub's own body; none is the request's own credential, and this
			// asserts the boundary rather than trusting it.
			if strings.Contains(strings.ToLower(got), "bearer ") {
				t.Errorf("WhyRefused said %q, which carries an authorization header", got)
			}
		})
	}
}
