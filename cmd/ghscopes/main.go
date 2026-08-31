// ghscopes says which account a token belongs to and what it is allowed to do,
// without ever showing the token.
//
// It exists because the failure that started all of this was a SCOPE: the token
// git was already using could not push a workflow file, and the only obvious way
// to find that out was to try a push and read the refusal. Checking beforehand
// meant putting the token in a curl command line — which is the mistake itself.
//
// So this reads the token from the file, sends it in a header, and prints only
// what came back. The token never reaches argv, an environment variable, or the
// output.
//
//	ghscopes                 what ~/.github-token may do
//	ghscopes repo workflow   the same, and fail unless it may do those
//	ghscopes -f ~/.renovate-token repo
//
// It also names the other token files it can see, because a token being checked
// is rarely the only one there is. Choosing the wrong one is not a hypothetical:
// a scoped ~/.renovate-token sat unnoticed beside the account-wide
// ~/.github-token while the wide one was written into a hundred repository
// secrets that only ever needed repo and workflow. Nothing said the other
// existed.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// defaultToken is the same file the credential helper serves from, so with no
// argument this checks the token git will actually use rather than some other
// one.
const defaultToken = ".github-token"

// osExit is a variable so a test can reach the exit path without ending the
// test binary.
var osExit = os.Exit

// userHomeDir is a variable for the same reason userURL is: this tool guards a
// security property, and every way it can refuse should be reachable from a
// test rather than only from a machine in an unusual state.
var userHomeDir = os.UserHomeDir

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	// Where home is decided once. It was asked for in three places, which is
	// three ways of not having one and one behaviour.
	home, err := userHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "ghscopes: no home directory: %v\n", err)
		return 1
	}
	path, want, err := parse(home, args)
	if err != nil {
		fmt.Fprintf(stderr, "ghscopes: %v\n", err)
		return 2
	}

	tok, err := readToken(path)
	if err != nil {
		fmt.Fprintf(stderr, "ghscopes: %v\n", err)
		return 1
	}
	login, scopes, err := identify(tok)
	if err != nil {
		fmt.Fprintf(stderr, "ghscopes: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "file:    %s\n", path)
	fmt.Fprintf(stdout, "account: %s\n", login)
	fmt.Fprintf(stdout, "scopes:  %s\n", strings.Join(scopes, ", "))
	if others := otherTokens(home, path); len(others) > 0 {
		fmt.Fprintf(stdout, "others:  %s\n", strings.Join(others, ", "))
	}

	missing := missingFrom(scopes, want)
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "ghscopes: missing scope(s): %s\n", strings.Join(missing, ", "))
		return 1
	}
	if len(want) > 0 {
		fmt.Fprintf(stdout, "has:     %s\n", strings.Join(want, ", "))
	}
	return 0
}

// parse separates the token file from the scopes demanded. A bare argument is a
// scope, as it always was; -f or --file names another token to look at.
func parse(home string, args []string) (path string, want []string, err error) {
	path = filepath.Join(home, defaultToken)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--file":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s needs the path of a token file", args[i])
			}
			i++
			path = args[i]
		default:
			want = append(want, args[i])
		}
	}
	return path, want, nil
}

// otherTokens names the token files beside the one being checked, so that
// choosing the wrong one is at least a choice. Only the names: a name says a
// token exists, and nothing about what it is.
func otherTokens(home, checked string) []string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	abs, _ := filepath.Abs(checked)
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, "-token") {
			continue
		}
		full := filepath.Join(home, name)
		if full == abs {
			continue
		}
		out = append(out, full)
	}
	sort.Strings(out)
	return out
}

// missingFrom reports which of want is absent from have.
func missingFrom(have, want []string) []string {
	set := make(map[string]bool, len(have))
	for _, s := range have {
		set[s] = true
	}
	var missing []string
	for _, w := range want {
		if !set[w] {
			missing = append(missing, w)
		}
	}
	sort.Strings(missing)
	return missing
}

// scopesHeader is where GitHub reports what a classic token may do. A
// fine-grained token sends it EMPTY, which is not the same as "no permissions" —
// so an empty header is reported as unknown rather than as a refusal.
const scopesHeader = "X-OAuth-Scopes"

// userURL is a variable so a test can answer as GitHub would. What this tool
// does with the answer is the part worth testing, and it guards a security
// property: a wrong reading here is how a token with more rights than the job
// needs gets used anyway.
var userURL = "https://api.github.com/user"

func identify(token string) (login string, scopes []string, err error) {
	req, err := http.NewRequest(http.MethodGet, userURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		// A transport error can quote the request URL but never a header, so
		// there is nothing here that could carry the token.
		return "", nil, fmt.Errorf("asking GitHub who this is: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("GitHub answered %s — the token is probably expired or revoked", resp.Status)
	}
	var body struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", nil, fmt.Errorf("reading GitHub's answer: %w", err)
	}

	raw := strings.TrimSpace(resp.Header.Get(scopesHeader))
	if raw == "" {
		return body.Login, []string{"(none reported — a fine-grained token lists no classic scopes)"}, nil
	}
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, s)
		}
	}
	sort.Strings(scopes)
	return body.Login, scopes, nil
}

// readToken never puts the token in an error, so a failure cannot leak what a
// success would have protected.
func readToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return tok, nil
}
