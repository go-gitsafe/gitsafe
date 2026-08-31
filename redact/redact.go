// Package redact removes secrets from text that is about to be shown to
// somebody.
//
// It exists because a token was pasted into a command line twice, and both times
// git echoed it straight back. The lesson is not "be more careful": a secret
// that CAN reach an output stream eventually will. So nothing here trusts the
// caller to have been careful — it assumes the text may contain a secret and
// takes it out.
package redact

import (
	"regexp"
	"sort"
	"strings"
)

// Mask is what replaces a secret. It is deliberately unmistakable, so that a
// redacted transcript reads as redacted rather than as a truncated token.
const Mask = "«REDACTED»"

// shapes are the token formats GitHub issues. They are matched even when the
// exact value is not known, because a secret can arrive from somewhere the
// caller never read — a nested command, an error message, a remote's own reply.
//
//   - gh[pousr]_… : classic personal access, OAuth, user-to-server,
//     server-to-server and refresh tokens
//   - github_pat_… : fine-grained personal access tokens
//   - x-access-token:… : the form that appears inside a URL, which is exactly
//     how both leaks happened
var shapes = []*regexp.Regexp{
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{16,}`),
	regexp.MustCompile(`(?i)://[^/@\s:]+:[^/@\s]+@`),
}

// Redactor removes both the secrets it was told about and anything shaped like
// one.
type Redactor struct{ literals []string }

// New returns a Redactor that also removes these exact values, longest first so
// that a secret containing another is not left half-masked.
func New(secrets ...string) *Redactor {
	var lits []string
	for _, s := range secrets {
		// A short "secret" would mask ordinary text everywhere. Anything that
		// short is not a credential, and masking it would do more harm than the
		// leak it prevents.
		if len(strings.TrimSpace(s)) >= 8 {
			lits = append(lits, strings.TrimSpace(s))
		}
	}
	sort.Slice(lits, func(i, j int) bool { return len(lits[i]) > len(lits[j]) })
	return &Redactor{literals: lits}
}

// String returns s with every known and every plausible secret removed.
func (r *Redactor) String(s string) string {
	for _, lit := range r.literals {
		s = strings.ReplaceAll(s, lit, Mask)
	}
	for _, re := range shapes {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			// The URL shape carries the scheme and the host around the secret;
			// keeping them makes the redacted line still readable.
			if strings.HasPrefix(m, "://") {
				return "://" + Mask + "@"
			}
			return Mask
		})
	}
	return s
}

// Bytes is String for a byte slice.
func (r *Redactor) Bytes(b []byte) []byte { return []byte(r.String(string(b))) }

// Clean reports whether s is free of anything that looks like a secret. It is
// used to refuse an action rather than to describe one — a remote URL that
// carries a credential must be fixed, not printed.
func Clean(s string) bool {
	for _, re := range shapes {
		if re.MatchString(s) {
			return false
		}
	}
	return true
}
