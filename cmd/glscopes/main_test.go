package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-gitsafe/gitsafe/glauth"
)

var fakeToken = "gl" + "pat-" + strings.Repeat("Ab0_", 9) + ".01.0w17y347e"

func setup(t *testing.T, self string) []string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(glauth.HeaderName) != fakeToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v4/personal_access_tokens/self":
			w.Write([]byte(self))
		case "/api/v4/user":
			w.Write([]byte(`{"username":"someone"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	newClient = func(host, tok string) *glauth.Client { return glauth.New(srv.URL, tok) }
	now = func() time.Time { return time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC) }
	file := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(file, []byte("PRIVATE-TOKEN: "+fakeToken+"\n"), 0o600)
	return []string{"-host", "gl.example", "-f", file}
}

func runIt(t *testing.T, args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := run(args, t.TempDir(), &out, &errb)
	all := out.String() + errb.String()
	if strings.Contains(all, "Ab0_") || strings.Contains(all, "0w17y347e") {
		t.Errorf("the token reached the output:\n%s", all)
	}
	return code, all
}

const readAPI = `{"name":"claude-read-api","scopes":["read_api"],"expires_at":"2026-11-03","active":true,"revoked":false}`

func TestReportsWithoutTheToken(t *testing.T) {
	code, out := runIt(t, setup(t, readAPI)...)
	for _, want := range []string{"account:  someone", "token:    claude-read-api", "scopes:   read_api", "2026-11-03 (in 30 days)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if code != 0 {
		t.Errorf("code %d", code)
	}
}

func TestDemandedScopes(t *testing.T) {
	args := setup(t, readAPI)
	if code, out := runIt(t, append(args, "read_api")...); code != 0 {
		t.Errorf("read_api refused: %d\n%s", code, out)
	}
	if code, out := runIt(t, append(args, "api")...); code != 1 || !strings.Contains(out, "missing scope(s): api") {
		t.Errorf("api granted to a read_api token: %d\n%s", code, out)
	}
	args = setup(t, `{"name":"w","scopes":["api"],"expires_at":"","active":true}`)
	if code, out := runIt(t, append(args, "read_api")...); code != 0 || !strings.Contains(out, "expires:  never") {
		t.Errorf("api does contain read_api: %d\n%s", code, out)
	}
}

func TestInactiveToken(t *testing.T) {
	code, out := runIt(t, setup(t, `{"name":"old","scopes":["read_api"],"expires_at":"2026-10-01","active":false,"revoked":true}`)...)
	if code != 1 || !strings.Contains(out, "(expired)") || !strings.Contains(out, "not active") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestWrongToken(t *testing.T) {
	args := setup(t, readAPI)
	os.WriteFile(args[3], []byte("gl"+"pat-"+strings.Repeat("Q", 20)), 0o600)
	code, out := runIt(t, args...)
	if code != 1 || !strings.Contains(out, "401") {
		t.Errorf("code %d\n%s", code, out)
	}
}
