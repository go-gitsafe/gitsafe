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

	"github.com/go-gitsafe/gitsafe/credurl"
)

// Mask is what replaces a secret. It is deliberately unmistakable, so that a
// redacted transcript reads as redacted rather than as a truncated token.
const Mask = "«REDACTED»"

// shapes are the credential formats matched even when the exact value is not
// known, because a secret can arrive from somewhere the caller never read — a
// nested command, an error message, a remote's own reply.
//
// The issuer prefixes come from [credurl.TokenPattern] rather than from a list
// kept here. There used to be a list here and it knew GitHub and nothing else,
// so Clean answered "clean" for a GitLab token in a URL and the global pre-push
// hook would have pushed it without a word. Two lists of prefixes in two
// packages is how one of them ends up missing the prefix that leaks.
//
// The second shape is the credential-in-a-URL form, which is what both leaks on
// this machine looked like. It stays here because it is about masking a STRING
// rather than judging a URL; the judgement lives in [credurl].
var shapes = []*regexp.Regexp{
	credurl.TokenPattern(16),
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
	// And a URL is put to [credurl] as well, because the shapes above catch a
	// credential whose ISSUER is known and a URL can carry one from an issuer
	// nobody here has heard of. That half was missing.
	return !credurl.Inspect(s).Leak()
}
