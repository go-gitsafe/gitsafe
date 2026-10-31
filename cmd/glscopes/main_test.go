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

	"github.com/go-gitsafe/gitsafe/cliflags"
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

// ⛔ ALL FOUR SPELLINGS, THROUGH THE COMMAND A PERSON GETS. It is driven in
// process rather than as a binary on purpose: this reaches cobra's parser and
// the single-dash adapter without any chance of a run doing real work.
//
// `-help` is the one cobra alone gets wrong (command.go:921 refuses the parse
// before the help check at :926), and `help` is the one it never declares for
// a command with no subcommands (command.go:1264).
func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help", "help"} {
		var out, errb bytes.Buffer
		cmd := RootCmd(t.TempDir())
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if code := cliflags.Run(cmd, []string{arg}); code != 0 {
			t.Errorf("glscopes %s exited %d\nstderr: %s", arg, code, errb.String())
		}
		for _, want := range []string{"--host", "--file"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("glscopes %s does not name %s:\n%s", arg, want, out.String())
			}
		}
	}
}

// ⛔⛔ THE ONE DECLARED SHORTHAND IN THIS REPOSITORY IS ALSO THE TRAP, and it
// is in this command. `--file` carries `-f`, so pflag reads `-file /tmp/tok`
// as `-f` with the value "ile" -- the rest of the cluster -- and leaves
// /tmp/tok behind as an operand, which glscopes then checks as a DEMANDED
// SCOPE. It does not fail. It answers a different question, with a token in
// hand.
//
// Both halves are asserted here: the adapter keeps the spelling intact, and
// the control confirms that pflag really does read it the other way, so this
// test cannot quietly become a tautology if the adapter stops being called.
func TestTheSingleDashFileSpellingIsNotTheShorthand(t *testing.T) {
	cmd := RootCmd(t.TempDir())
	if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, []string{"-file", "/tmp/tok"})); err != nil {
		t.Fatalf("-file through the adapter: %v", err)
	}
	if got, _ := cmd.Flags().GetString("file"); got != "/tmp/tok" {
		t.Errorf("file = %q, want /tmp/tok", got)
	}
	if n := cmd.Flags().NArg(); n != 0 {
		t.Errorf("left %d operand(s) behind, which would be read as demanded scopes: %q",
			n, cmd.Flags().Args())
	}

	bare := RootCmd(t.TempDir())
	if err := bare.Flags().Parse([]string{"-file", "/tmp/tok"}); err != nil {
		t.Fatalf("control: expected pflag to ACCEPT -file as a cluster, got %v", err)
	}
	if got, _ := bare.Flags().GetString("file"); got != "ile" {
		t.Errorf("control: without the adapter, file = %q, want \"ile\": pflag changed, "+
			"or this control is measuring nothing", got)
	}
	if args := bare.Flags().Args(); len(args) != 1 || args[0] != "/tmp/tok" {
		t.Errorf("control: operands = %q, want [/tmp/tok] left over as a scope", args)
	}
}

// ⛔ THE SPELLINGS THIS TOOL'S OWN DOCUMENTATION USES. Every example in the
// package comment above is single-dash, and pflag reads a single dash as a
// cluster of shorthands -- `-host` as h, o, s, t. The other tests here pass
// `-host` and `-f` too, which is why they are the real witnesses; this one
// says so out loud, and adds the double-dash form cobra brings with it.
func TestBothSpellingsOfEveryFlag(t *testing.T) {
	for _, spelling := range [][]string{
		{"-host", "gl.example"},
		{"--host", "gl.example"},
		{"-host=gl.example"},
		{"--host=gl.example"},
	} {
		cmd := RootCmd(t.TempDir())
		args := cliflags.LegacyArgs(cmd, spelling)
		if err := cmd.Flags().Parse(args); err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if got, _ := cmd.Flags().GetString("host"); got != "gl.example" {
			t.Errorf("%q set host to %q, want gl.example", spelling, got)
		}
	}
}

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
