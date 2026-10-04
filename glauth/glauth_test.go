package glauth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeToken is assembled, not written whole, so nothing scanning this
// repository finds something shaped like a credential. It has the routable
// shape (dots inside), the one that leaked.
var fakeToken = "gl" + "pat-" + strings.Repeat("Ab0_", 9) + ".01.0w17y347e"

// TestReadAcceptsBothShapes is the incident itself: the file was expected to
// hold "PRIVATE-TOKEN: <token>" and held the bare token.
func TestReadAcceptsBothShapes(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"bare":          fakeToken + "\n",
		"header":        "PRIVATE-TOKEN: " + fakeToken + "\n",
		"header-lower":  "private-token:" + fakeToken,
		"blank-padding": "\n\n  " + fakeToken + "  \n\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Read(p)
		if err != nil || got != fakeToken {
			t.Errorf("%s: read a token=%v, equal=%v, err=%v", name, got != "", got == fakeToken, err)
		}
	}
}

// TestReadNeverQuotesTheFile: every refusal names the file and the shape it
// expected, never what it found.
func TestReadNeverQuotesTheFile(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"two-lines": fakeToken + "\n" + fakeToken + "\n",
		"spaces":    "my token is " + fakeToken,
		"empty":     "\n",
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o600)
		_, err := Read(p)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "Ab0_") || strings.Contains(err.Error(), "0w17y347e") {
			t.Errorf("%s: the error quotes the token: %v", name, err)
		}
	}
}

// TestTheTokenOnlyGoesIntoTheHeader: not the URL, not the query, not an error.
func TestTheTokenOnlyGoesIntoTheHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.String(), "Ab0_") {
			t.Errorf("token in the URL")
		}
		if r.Header.Get(HeaderName) != fakeToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v4/personal_access_tokens/self":
			w.Write([]byte(`{"name":"ci-read","scopes":["read_api"],"expires_at":"2026-11-03","active":true}`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, fakeToken)
	tok, err := c.Self()
	if err != nil || tok.Name != "ci-read" || len(tok.Scopes) != 1 {
		t.Fatalf("Self: %+v, %v", tok, err)
	}
	_, err = c.Get("/projects/x/pipelines?ref=main")
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "Ab0_") {
		t.Errorf("refusal: %v", err)
	}
	if strings.Contains(err.Error(), "ref=main") {
		t.Errorf("the refusal quotes the query: %v", err)
	}
	_, err = New(srv.URL, "wrong").Self()
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a wrong token: %v", err)
	}
}

// TestScopeContainments are the two measured in GitLab's source, and the one a
// GitHub-shaped list would have got wrong.
func TestScopeContainments(t *testing.T) {
	for _, tc := range []struct {
		have, want, missing []string
	}{
		{[]string{"api"}, []string{"read_api"}, nil},
		{[]string{"read_api"}, []string{"api"}, []string{"api"}},
		{[]string{"write_repository"}, []string{"read_repository"}, nil},
		{[]string{"write_registry"}, []string{"read_registry"}, []string{"read_registry"}},
		{[]string{"read_api", "read_repository"}, []string{"read_api", "read_repository"}, nil},
	} {
		got := Missing(tc.have, tc.want)
		if strings.Join(got, ",") != strings.Join(tc.missing, ",") {
			t.Errorf("Missing(%v, %v) = %v, want %v", tc.have, tc.want, got, tc.missing)
		}
	}
}

func TestParseRemote(t *testing.T) {
	for raw, want := range map[string]Remote{
		"git@plmlab.math.cnrs.fr:resinfo/gt/gt-cloud/docs.git": {"plmlab.math.cnrs.fr", "resinfo/gt/gt-cloud/docs"},
		"ssh://git@gitlab.example.org:2222/group/project.git":  {"gitlab.example.org", "group/project"},
		"https://gitlab.com/group/sub/project.git":             {"gitlab.com", "group/sub/project"},
		"https://gitlab.com/group/project/":                    {"gitlab.com", "group/project"},
		"https://oauth2@gitlab.com/group/project.git":          {"gitlab.com", "group/project"},
	} {
		got, ok := ParseRemote(raw)
		if !ok || got != want {
			t.Errorf("ParseRemote(%q) = %+v, %v; want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"", "not a url", "https://gitlab.com/only-one-segment",
		"https://oauth2:" + fakeToken + "@gitlab.com/group/project.git",
	} {
		if _, ok := ParseRemote(raw); ok {
			t.Errorf("ParseRemote accepted %q", strings.ReplaceAll(raw, fakeToken, "«token»"))
		}
	}
}

func TestTokenPathPrefersThePerHostFile(t *testing.T) {
	home := t.TempDir()
	if got := TokenPath(home, "plmlab.math.cnrs.fr", ""); got != filepath.Join(home, ".gitlab-token") {
		t.Errorf("no per-host file: %s", got)
	}
	perHost := filepath.Join(home, ".gitlab-token-plmlab.math.cnrs.fr")
	os.WriteFile(perHost, []byte("x"), 0o600)
	if got := TokenPath(home, "plmlab.math.cnrs.fr", ""); got != perHost {
		t.Errorf("per-host file ignored: %s", got)
	}
	if got := TokenPath(home, "plmlab.math.cnrs.fr", "/explicit"); got != "/explicit" {
		t.Errorf("explicit ignored: %s", got)
	}
}
