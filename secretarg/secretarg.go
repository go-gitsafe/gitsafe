// Package secretarg answers one question: does this command line put a secret
// where every process on the machine can read it?
//
// A command line reaches the process list, the shell history, and every log of
// what ran. Two GitHub tokens have had to be revoked on this machine because
// one ended up there. The written rule did not prevent the third occurrence:
// the same operator, hours after re-reading the rule and writing it down again,
// typed
//
//	curl -H "Authorization: Bearer $(cat ~/.github-token)" …
//
// about thirty times in one session. It reads as careful — the secret is never
// typed — and it is not: the substitution runs first, so what reaches execve is
// the token itself.
//
// So this is a package rather than a paragraph. It is used by the harness guard
// that refuses such a command before it runs, and it can be used by anything
// else that wants to ask the same question.
//
// # What it will not do
//
// It does not refuse the safe ways to use a secret, and that restraint is the
// point rather than a concession. A guard that refuses harmless commands is one
// people learn to work around, and then it protects nothing at all — this
// machine has already had that happen to a rule matching "push" too broadly.
// So these stay allowed:
//
//	gh api repos/o/r --jq .full_name        # the tool holds its own credential
//	{ printf 'header = "…'; tr -d '\n' < ~/.token; } | curl -K -
//	wc -c < ~/.github-token                 # a property, never the value
//
// and so does WRITING about the forbidden form: the body of a heredoc whose tag
// is quoted is never expanded by the shell, so a commit message or a note that
// quotes it is not it.
package secretarg

import (
	"regexp"
	"strings"
)

// Finding is why a command line was refused. The zero value means nothing was
// found.
type Finding struct {
	// Rule names what matched, for a caller that wants to distinguish them.
	Rule string
	// Match is the fragment of the command that triggered it, so a refusal can
	// point at something rather than assert.
	Match string
	// Why is one sentence a person can act on.
	Why string
}

// Found reports whether anything was found.
func (f Finding) Found() bool { return f.Rule != "" }

var (
	// literal token shapes: a secret typed out in full.
	literal = regexp.MustCompile(`ghp_[A-Za-z0-9]{8,}|github_pat_[A-Za-z0-9_]{8,}|gho_[A-Za-z0-9]{8,}|ghs_[A-Za-z0-9]{8,}|xox[baprs]-[A-Za-z0-9-]{8,}|AKIA[0-9A-Z]{12,}|x-access-token:[^@\s]+`)

	// A file whose NAME says it holds a credential. Matching the name rather
	// than the contents is deliberate: the contents are exactly what must not be
	// read in order to decide.
	secretFile = regexp.MustCompile(`(?i)(token|secret|credential|passwd|password|apikey|api_key|\.pem|id_[dre]sa|\.netrc|\.npmrc|\.pgpass)`)

	// A command substitution, $(…) or `…`, and the $(< file) form.
	subst = regexp.MustCompile("\\$\\(([^()]*)\\)|`([^`]*)`")

	// Commands that read a file's CONTENTS. `wc -c < f` and `ls f` do not.
	readers = regexp.MustCompile(`(?i)(^|[|;&\s(])(cat|head|tail|tr|cut|sed|awk|xargs|printf|echo|base64|jq|openssl|<)\b|^\s*<`)

	// A quoted heredoc tag: <<'EOF', <<"EOF", <<\EOF, with an optional dash.
	heredocOpen = regexp.MustCompile(`<<-?\s*(?:'([A-Za-z_][A-Za-z0-9_]*)'|"([A-Za-z_][A-Za-z0-9_]*)"|\\([A-Za-z_][A-Za-z0-9_]*))`)
)

// Check reports whether cmd discloses a secret on the command line.
func Check(cmd string) Finding {
	scanned := stripQuotedHeredocs(cmd)
	if m := literal.FindString(scanned); m != "" {
		return Finding{
			Rule:  "literal",
			Match: m,
			Why: "this carries what looks like a credential on the command line, " +
				"which reaches the process list, the shell history and every log of what ran",
		}
	}
	for _, m := range subst.FindAllStringSubmatch(scanned, -1) {
		body := m[1]
		if body == "" {
			body = m[2]
		}
		if body == "" || !readers.MatchString(body) {
			continue
		}
		if f := secretFile.FindString(body); f != "" {
			return Finding{
				Rule:  "substitution",
				Match: strings.TrimSpace(m[0]),
				Why: "this reads a secret INTO the command line: the substitution runs " +
					"before the command does, so what reaches execve is the secret itself",
			}
		}
	}
	return Finding{}
}

// stripQuotedHeredocs removes the body of every heredoc whose tag is quoted.
//
// `<<'EOF'` is literal — the shell expands nothing inside it — so text that
// QUOTES the forbidden form is not the forbidden form. Documentation, a commit
// message and a note all need to say what not to do. An unquoted `<<EOF` does
// expand, and its body is left in place to be scanned.
func stripQuotedHeredocs(cmd string) string {
	lines := strings.Split(cmd, "\n")
	var out []string
	tag := ""
	for _, line := range lines {
		if tag != "" {
			if strings.TrimSpace(line) == tag {
				tag = ""
			}
			continue
		}
		if m := heredocOpen.FindStringSubmatch(line); m != nil {
			for _, g := range m[1:] {
				if g != "" {
					tag = g
					break
				}
			}
			// The opening line itself is kept: a command may both open a heredoc
			// and carry a substitution of its own.
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Advice is what to do instead, printed with a refusal.
const Advice = `Send it down a pipe, or let a tool hold it:

    gh api repos/o/r --jq .full_name          # gh reads its own credential
    { printf 'header = "Authorization: Bearer '
      tr -d '\n' < ~/.github-token
      printf '"\n'; } | curl -s -K -          # the secret never enters argv
    gitpush origin main                       # never reads the token at all
    ghscopes                                  # checks it by its properties`
