// Package ghauth answers two questions about a GitHub credential: how to read
// it without ever putting it somewhere it could be echoed, and what it may do.
//
// It exists because the answers were written three times — in ghscopes, in
// ghmerge, and in ghrelease — and a fourth command was about to write them a
// fourth time. A rule restated at four call sites is a rule that drifts, and
// this one guards a secret.
package ghauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
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

// WhyRefused explains a GitHub answer that is not a success, in the terms a
// person can act on.
//
// ⛔⛔ EVERY NON-SUCCESS USED TO READ AS A DEAD CREDENTIAL. ghscopes collapsed
// them all into "the token is probably expired or revoked", and ghpkg said the
// same words. That is one of four different situations, and naming the wrong one
// sends somebody to revoke and reissue a token that was working.
//
// ⭐ IT HAPPENED, AND THE EVIDENCE WAS ALREADY IN HAND. After seven pull
// requests merged in quick succession, ghmerge and then ghscopes both answered
// 403 while `gitpush --dry-run` on the same token answered "Everything
// up-to-date" -- so git was authenticating with it one second and the API was
// refusing it the next. It was GitHub's BURST limit. The measurement said the
// credential was alive and the tool's label said it was dead, and the label won.
//
// GitHub does distinguish these, so this does too:
//
//	401, or "bad credentials"        the credential itself
//	"secondary rate limit", "abuse"  the burst limit; wait and retry
//	Retry-After present              the same, and it says how long
//	403 with no budget left          the hourly budget; it says when it resets
//	anything else                    the status, and GitHub's own sentence
//
// ⚠ NOTHING HERE CAN CARRY THE TOKEN. The status, the headers this reads and
// GitHub's `message` field are all answers ABOUT a request, never the request's
// own Authorization header -- which is why the body may be quoted at all.
func WhyRefused(status int, header http.Header, body []byte) error {
	var msg struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &msg)
	lower := strings.ToLower(msg.Message)

	switch {
	case status == http.StatusUnauthorized || strings.Contains(lower, "bad credentials"):
		return fmt.Errorf("GitHub refused the credential (%d): it is expired, "+
			"revoked, or not a token at all", status)

	case strings.Contains(lower, "secondary rate limit"),
		strings.Contains(lower, "abuse detection"),
		header.Get("Retry-After") != "":
		// ⭐ THE WAIT IS GITHUB'S OWN NUMBER when it gives one, because the
		// alternative is a person guessing, and the guess that feels safe is
		// "the token is broken".
		if s := header.Get("Retry-After"); s != "" {
			return fmt.Errorf("GitHub's burst limit (%d): too many writes too "+
				"quickly. It asks for %s seconds; the credential is fine", status, s)
		}
		return fmt.Errorf("GitHub's burst limit (%d): too many writes too "+
			"quickly. Wait a minute and retry; the credential is fine", status)

	case header.Get("X-RateLimit-Remaining") == "0":
		if s := header.Get("X-RateLimit-Reset"); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return fmt.Errorf("GitHub's hourly budget is spent (%d): it "+
					"resets at %s; the credential is fine",
					status, time.Unix(n, 0).Format(time.TimeOnly))
			}
		}
		return fmt.Errorf("GitHub's hourly budget is spent (%d); the "+
			"credential is fine", status)
	}
	if msg.Message != "" {
		return fmt.Errorf("GitHub answered %d: %s", status, msg.Message)
	}
	return fmt.Errorf("GitHub answered %d", status)
}
