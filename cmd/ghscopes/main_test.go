package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMissingFrom(t *testing.T) {
	have := []string{"repo", "workflow", "gist"}
	for _, tc := range []struct {
		name string
		want []string
		miss []string
	}{
		{"nothing demanded", nil, nil},
		{"all present", []string{"repo", "workflow"}, nil},
		{"one absent", []string{"repo", "admin:org"}, []string{"admin:org"}},
		{"several absent, sorted", []string{"write:packages", "admin:org"}, []string{"admin:org", "write:packages"}},
		{"a scope is not a prefix match", []string{"work"}, []string{"work"}},
	} {
		if got := missingFrom(have, tc.want); !reflect.DeepEqual(got, tc.miss) {
			t.Errorf("%s: missingFrom(%q, %q) = %q, want %q", tc.name, have, tc.want, got, tc.miss)
		}
	}
	// A token that reports no scopes at all satisfies nothing, rather than
	// everything — the safe direction when the answer is unknown.
	if got := missingFrom(nil, []string{"workflow"}); !reflect.DeepEqual(got, []string{"workflow"}) {
		t.Errorf("with no scopes reported, missingFrom = %q, want the demand unmet", got)
	}
}

func TestParseSeparatesTheFileFromTheScopes(t *testing.T) {
	home := t.TempDir()
	def := filepath.Join(home, defaultToken)
	for _, tc := range []struct {
		name string
		args []string
		path string
		want []string
	}{
		{"nothing at all", nil, def, nil},
		{"scopes only, as it always was", []string{"repo", "workflow"}, def, []string{"repo", "workflow"}},
		{"a file, short", []string{"-f", "/tmp/t"}, "/tmp/t", nil},
		{"a file, long", []string{"--file", "/tmp/t"}, "/tmp/t", nil},
		{"a file and scopes, either way round",
			[]string{"repo", "-f", "/tmp/t", "workflow"}, "/tmp/t", []string{"repo", "workflow"}},
		{"the last file named wins", []string{"-f", "/tmp/a", "-f", "/tmp/b"}, "/tmp/b", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, want, err := parse(home, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if path != tc.path {
				t.Errorf("file %q, want %q", path, tc.path)
			}
			if !reflect.DeepEqual(want, tc.want) {
				t.Errorf("scopes %q, want %q", want, tc.want)
			}
		})
	}
}

func TestAFileFlagWithNoFile(t *testing.T) {
	// Taking the next argument as a path when there is none would read the
	// scope the caller asked for as a filename, and say the token file is
	// missing rather than that the command is.
	for _, flag := range []string{"-f", "--file"} {
		if _, _, err := parse(t.TempDir(), []string{flag}); err == nil {
			t.Errorf("%s alone was accepted", flag)
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"-f"}, &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "needs the path") {
		t.Errorf("it said %q", errOut.String())
	}
}

func TestATokenFileThatIsNotThere(t *testing.T) {
	var out, errOut bytes.Buffer
	missing := filepath.Join(t.TempDir(), "gone")
	if code := run([]string{"-f", missing}, &out, &errOut); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "cannot read") {
		t.Errorf("it said %q", errOut.String())
	}
}

func TestAnEmptyTokenFile(t *testing.T) {
	// An empty file is not a token, and saying so beats asking GitHub who
	// nobody is.
	p := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(p, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(p); err == nil {
		t.Error("an empty file was accepted as a token")
	}
}

func TestATokenIsReadWithoutItsWhitespace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("  abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readToken(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc" {
		t.Errorf("read %q", got)
	}
}

func TestTheOtherTokensAreNamed(t *testing.T) {
	// A token being checked is rarely the only one there is, and choosing the
	// wrong one is not hypothetical: a scoped renovate token sat unnoticed
	// beside the account-wide one while the wide one went into a hundred
	// repository secrets that needed two of its seven scopes.
	//
	// Only the names. A name says a token exists and nothing about what it is.
	home := t.TempDir()
	for _, name := range []string{".github-token", ".renovate-token", ".ssh", "notes.txt", "token"} {
		if name == ".ssh" {
			os.Mkdir(filepath.Join(home, name), 0o700)
			continue
		}
		os.WriteFile(filepath.Join(home, name), []byte("x"), 0o600)
	}
	got := otherTokens(home, filepath.Join(home, ".github-token"))
	if len(got) != 1 || filepath.Base(got[0]) != ".renovate-token" {
		t.Fatalf("named %v", got)
	}
	// The one being checked is not among the others.
	for _, p := range got {
		if filepath.Base(p) == ".github-token" {
			t.Errorf("the file being checked is listed as another: %s", p)
		}
	}
	// A directory whose name ends in -token is not a token, and nor is a
	// file that does not begin with a dot.
	if names := otherTokens(filepath.Join(home, "nowhere"), ""); names != nil {
		t.Errorf("a home that is not there named %v", names)
	}
}

func TestMainIsWiredToRun(t *testing.T) {
	was := osExit
	defer func() { osExit = was }()
	code := -1
	osExit = func(c int) { code = c }
	old := os.Args
	os.Args = []string{"ghscopes", "-f"}
	defer func() { os.Args = old }()
	main()
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestSomewhereThatCannotBeAsked(t *testing.T) {
	was := userURL
	t.Cleanup(func() { userURL = was })
	userURL = "://not a url"
	var out, errOut bytes.Buffer
	if code := run([]string{"-f", aToken(t)}, &out, &errOut); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

// answering stands in for GitHub, so what this does with an answer can be
// checked without one.
func answering(t *testing.T, status int, scopes, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer abc" {
			t.Errorf("the token reached GitHub as %q", got)
		}
		if scopes != "" {
			w.Header().Set(scopesHeader, scopes)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	was := userURL
	t.Cleanup(func() { userURL = was })
	userURL = srv.URL
}

// aToken writes a token file and returns its path.
func aToken(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWhatComesBackIsReported(t *testing.T) {
	answering(t, 200, "repo, workflow", `{"login":"tannevaled"}`)
	var out, errOut bytes.Buffer
	if code := run([]string{"-f", aToken(t), "repo"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	for _, want := range []string{"tannevaled", "repo, workflow", "has:     repo", "file:"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in %q", want, got)
		}
	}
	// Whatever else it prints, never the token.
	if strings.Contains(got+errOut.String(), "abc") {
		t.Error("the token reached the output")
	}
}

func TestAScopeThatIsNotThereIsARefusal(t *testing.T) {
	answering(t, 200, "repo", `{"login":"tannevaled"}`)
	var out, errOut bytes.Buffer
	if code := run([]string{"-f", aToken(t), "workflow"}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "missing scope(s): workflow") {
		t.Errorf("it said %q", errOut.String())
	}
}

func TestATokenGitHubWillNotAnswerFor(t *testing.T) {
	answering(t, 401, "", `{}`)
	var out, errOut bytes.Buffer
	if code := run([]string{"-f", aToken(t)}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "expired or revoked") {
		t.Errorf("it said %q", errOut.String())
	}
}

func TestAnAnswerThatIsNotJSON(t *testing.T) {
	answering(t, 200, "repo", `not json`)
	var out, errOut bytes.Buffer
	if code := run([]string{"-f", aToken(t)}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "reading GitHub's answer") {
		t.Errorf("it said %q", errOut.String())
	}
}

func TestAFineGrainedTokenReportsNoScopes(t *testing.T) {
	// An empty header is not "no permissions": a fine-grained token simply
	// does not list classic scopes. Reading it as none would refuse a token
	// that can do the work.
	answering(t, 200, "", `{"login":"tannevaled"}`)
	var out, errOut bytes.Buffer
	if code := run([]string{"-f", aToken(t)}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "fine-grained") {
		t.Errorf("it said %q", out.String())
	}
}

func TestNobodyToAsk(t *testing.T) {
	was := userURL
	t.Cleanup(func() { userURL = was })
	userURL = "http://127.0.0.1:1"
	var out, errOut bytes.Buffer
	if code := run([]string{"-f", aToken(t)}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "asking GitHub") {
		t.Errorf("it said %q", errOut.String())
	}
}

func TestNoHomeToLookIn(t *testing.T) {
	// Every way this can refuse should be reachable from a test rather than
	// only from a machine in an unusual state.
	was := userHomeDir
	t.Cleanup(func() { userHomeDir = was })
	userHomeDir = func() (string, error) { return "", errors.New("nowhere to live") }
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "no home directory") {
		t.Errorf("it said %q", errOut.String())
	}
}
