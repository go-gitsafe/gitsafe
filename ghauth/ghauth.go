// Package ghauth answers two questions about a GitHub credential: how to read
// it without ever putting it somewhere it could be echoed, and what it may do.
//
// It exists because the answers were written three times — in ghscopes, in
// ghmerge, and in ghrelease — and a fourth command was about to write them a
// fourth time. A rule restated at four call sites is a rule that drifts, and
// this one guards a secret.
package ghauth

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultFile is the token this machine keeps apart from the wide one, so a
// command can name it without every caller repeating the string.
const DefaultFile = ".github-token"

// DefaultPath is DefaultFile under the user's home directory.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return DefaultFile
	}
	return filepath.Join(home, DefaultFile)
}

// Read returns the token in path.
//
// It never puts the token in an error, so a failure cannot leak what a success
// would have protected — and it never returns it through anything but its
// value, so a caller cannot accidentally hand it to a command line.
func Read(path string) (string, error) {
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

// Implies maps a classic OAuth scope onto the scopes it CONTAINS.
//
// GitHub's scopes are a hierarchy, not a list: granting `write:packages` grants
// `read:packages` with it, and the token's X-OAuth-Scopes header reports only
// the one that was ticked. A literal comparison therefore reports a scope as
// missing that the token plainly has — measured on a token carrying
// `delete:packages, write:packages`, reported as missing `read:packages`, and
// the reading was believed.
//
// From GitHub's "Scopes for OAuth apps". Only the containments are listed; a
// scope that contains nothing needs no entry.
var Implies = map[string][]string{
	"repo":             {"repo:status", "repo_deployment", "public_repo", "repo:invite", "security_events"},
	"write:packages":   {"read:packages"},
	"delete:packages":  {"read:packages"},
	"admin:org":        {"write:org", "read:org", "manage_runners:org"},
	"write:org":        {"read:org"},
	"admin:public_key": {"write:public_key", "read:public_key"},
	"write:public_key": {"read:public_key"},
	"admin:repo_hook":  {"write:repo_hook", "read:repo_hook"},
	"write:repo_hook":  {"read:repo_hook"},
	"user":             {"read:user", "user:email", "user:follow"},
	"admin:gpg_key":    {"write:gpg_key", "read:gpg_key"},
	"write:gpg_key":    {"read:gpg_key"},
	"project":          {"read:project"},
	"admin:enterprise": {"manage_runners:enterprise", "manage_billing:enterprise", "read:enterprise"},
	"write:discussion": {"read:discussion"},
	"codespace":        {"codespace:secrets"},
}

// Expand returns the scopes a token really has: the ones it was granted, plus
// everything those contain, transitively.
func Expand(granted []string) map[string]bool {
	set := map[string]bool{}
	var add func(string)
	add = func(s string) {
		if set[s] {
			return // already expanded; also what stops a cycle, should one appear
		}
		set[s] = true
		for _, sub := range Implies[s] {
			add(sub)
		}
	}
	for _, s := range granted {
		add(s)
	}
	return set
}

// Missing reports which of want the token cannot do — counting what its scopes
// CONTAIN, not only what they are called.
func Missing(have, want []string) []string {
	set := Expand(have)
	var missing []string
	for _, w := range want {
		if !set[w] {
			missing = append(missing, w)
		}
	}
	sort.Strings(missing)
	return missing
}
