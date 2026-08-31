// git-credential-tokenfile hands git a GitHub token without the token ever
// touching a command line, an environment variable, a config file or a terminal.
//
// It exists because a token was put into a push URL twice, and git echoed it
// back both times. A credential helper is the shape git actually intends for
// this: the secret travels from a file to git over a pipe, and there is no step
// in between where a person or a transcript can see it.
//
// Two refusals make that structural rather than merely intended:
//
//   - It answers only "get". A helper that can store or erase can be talked into
//     writing a secret somewhere new.
//   - It refuses to write anything when its output is a TERMINAL. Nothing about
//     git's use of it goes to a terminal, so the only way that happens is
//     somebody running it by hand to see what it says — which is precisely the
//     act that must not print a token.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tokenFile is the workflow-scoped token. The keyring token gh serves has no
// `workflow` scope, which is why pushing a .github/workflows change needs this
// one, and why reaching for a URL felt necessary. It is not.
const tokenFile = ".github-token"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin *os.File, stdout *os.File, stderr *os.File) int {
	if len(args) != 1 || args[0] != "get" {
		// store and erase are answered with silence, which git accepts: it means
		// "this helper has nothing to contribute".
		return 0
	}
	if isTerminal(stdout) {
		fmt.Fprintln(stderr, "git-credential-tokenfile: refusing to write a credential to a terminal")
		return 1
	}

	req := map[string]string{}
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			req[k] = v
		}
	}

	// Only GitHub, so a misconfigured helper cannot offer this token to some
	// other host that asks.
	if h := req["host"]; h != "" && h != "github.com" && !strings.HasSuffix(h, ".github.com") {
		return 0
	}

	tok, err := readToken()
	if err != nil {
		fmt.Fprintf(stderr, "git-credential-tokenfile: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "username=x-access-token\npassword=%s\n\n", tok)
	return 0
}

// readToken reads the token and never returns it inside an error, so a failure
// cannot leak what a success would have protected.
func readToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home directory: %w", err)
	}
	path := filepath.Join(home, tokenFile)
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

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		// Unknown is treated as a terminal: the safe answer when the question
		// cannot be answered is the one that refuses.
		return true
	}
	return st.Mode()&os.ModeCharDevice != 0
}
