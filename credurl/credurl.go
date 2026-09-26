// Package credurl answers one question about a git remote URL: is the part
// before the `@` a secret, or a username?
//
// It exists because a classic personal access token sat in the fetch AND push
// URL of 117 checkouts on one machine for three months, and nothing noticed.
// Git prints a remote URL on any fetch, so the token had to be treated as
// disclosed from the first day.
//
// # Why the question is harder than it looks
//
// Two detectors were written by hand for that incident and both were wrong, in
// OPPOSITE directions. Each failure is a test in this package:
//
//   - Matching `://user:TOKEN@host` missed 5 checkouts whose URL had no
//     username at all — `://TOKEN@host`. A credential does not need a colon.
//   - Matching any `://…@…` then flagged 55 URLs that were never leaks:
//     `ssh://git@github.com/o/r` and `git@plmlab.math.cnrs.fr:team/repo`.
//     There, `git` is an SSH login. A guard that calls those a leak is one
//     people switch off, and then it guards nothing.
//
// So the verdict is made on the SHAPE of what is in the userinfo, never on the
// presence of an `@`. Both directions matter equally: this package is as
// responsible for the URLs it calls clean as for the ones it refuses.
//
// # The value never comes back out
//
// A [Finding] names the host and the credential's PROPERTIES — issuer prefix,
// length, and a short digest so two findings can be told apart — and never the
// credential. The prefix comes from the fixed table below rather than from the
// input, so even a printed prefix cannot echo an unknown secret. Verify a
// credential by its properties, never by printing it: this machine has revoked
// two tokens that reached a terminal.
//
// The one exception is [Finding.Login], which carries a name only when that
// name is one of the benign logins in [BenignLogins]. Echoing a userinfo that
// this package decided was benign would leak precisely in the case where the
// decision was wrong, so nothing outside that fixed list is ever repeated.
package credurl

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"strings"
)

// Verdict is what the userinfo of a URL turned out to be.
type Verdict int

const (
	// NoUserinfo: there is nothing before an `@`, or there is no `@`.
	NoUserinfo Verdict = iota
	// Username: there is userinfo and it is a login name. `ssh://git@host/…`
	// and `git@host:path` are the overwhelming majority of these.
	Username
	// Secret: the userinfo carries a credential. This is a leak: git echoes a
	// remote URL, and the URL is written into .git/config where it stays.
	Secret
)

func (v Verdict) String() string {
	switch v {
	case Username:
		return "username"
	case Secret:
		return "secret"
	default:
		return "no userinfo"
	}
}

// Shape is one credential format, recognised by the prefix its issuer puts on
// it. A shape is deliberately a prefix plus a body charset plus a minimum
// length: that is what can be checked without knowing the value, and knowing
// the value is exactly what must not be required.
type Shape struct {
	// Name is what to call it in a report.
	Name string
	// Prefix is the issuer's marker, e.g. "ghp_". It is public information —
	// every token of that kind starts with it — so a report may print it.
	Prefix string
	// body is the character class of everything after the prefix.
	body string
	// min is the shortest body this shape ever has.
	min int
}

// shapes are the credential formats recognised by prefix.
//
// The list is an allowlist of KNOWN issuers, and [Generic] below is the
// fallback for the rest. Both are needed: the GitHub prefixes would have caught
// the 117 checkouts, and nothing prefix-based catches a credential from an
// issuer nobody here has heard of yet.
var shapes = []Shape{
	{"GitHub classic personal access token", "ghp_", `[A-Za-z0-9]`, 16},
	{"GitHub OAuth token", "gho_", `[A-Za-z0-9]`, 16},
	{"GitHub user-to-server token", "ghu_", `[A-Za-z0-9]`, 16},
	{"GitHub server-to-server token", "ghs_", `[A-Za-z0-9]`, 16},
	{"GitHub refresh token", "ghr_", `[A-Za-z0-9]`, 16},
	{"GitHub fine-grained personal access token", "github_pat_", `[A-Za-z0-9_]`, 16},
	{"GitLab personal access token", "glpat-", `[A-Za-z0-9_-]`, 16},
	{"GitLab deploy token", "gldt-", `[A-Za-z0-9_-]`, 16},
	{"GitLab runner token", "glrt-", `[A-Za-z0-9_-]`, 16},
	{"Slack bot token", "xoxb-", `[A-Za-z0-9-]`, 8},
	{"Slack user token", "xoxp-", `[A-Za-z0-9-]`, 8},
	{"Slack app-level token", "xoxa-", `[A-Za-z0-9-]`, 8},
	{"Slack refresh token", "xoxr-", `[A-Za-z0-9-]`, 8},
	{"Slack session token", "xoxs-", `[A-Za-z0-9-]`, 8},
	{"AWS access key id", "AKIA", `[0-9A-Z]`, 12},
	{"AWS temporary access key id", "ASIA", `[0-9A-Z]`, 12},
}

// Shapes returns the recognised credential formats.
//
// It is exported so that everything on this machine asks ONE table. Two lists
// of token prefixes in two packages is how one of them ends up missing the
// prefix that leaks: the pre-push hook and the command-line guard each had
// their own, and each was missing a different half of GitLab's.
func Shapes() []Shape {
	out := make([]Shape, len(shapes))
	copy(out, shapes)
	return out
}

// anchored matches a whole userinfo half against this shape.
func (s Shape) anchored() *regexp.Regexp { return s.compile(true, s.min) }

func (s Shape) compile(anchor bool, min int) *regexp.Regexp {
	pat := regexp.QuoteMeta(s.Prefix) + s.body + "{" + itoa(min) + ",}"
	if anchor {
		pat = `\A` + pat + `\z`
	}
	return regexp.MustCompile(pat)
}

// itoa avoids strconv for one small number, keeping the import list of a
// security-relevant package as short as it can be.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

var anchoredShapes = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(shapes))
	for i, s := range shapes {
		out[i] = s.anchored()
	}
	return out
}()

// TokenPattern is a regexp matching any known credential shape ANYWHERE in a
// text, for callers that scan a line rather than judge a URL — a redactor, a
// command-line guard.
//
// minBody is the shortest body to accept. A scanner that must not miss a
// truncated token passes something small; one that must not mangle ordinary
// prose passes the issuer's real minimum. The choice is the caller's because
// the cost of a false positive differs between them, but the PREFIXES are not,
// which is the whole point of asking here.
func TokenPattern(minBody int) *regexp.Regexp {
	parts := make([]string, 0, len(shapes))
	for _, s := range shapes {
		min := minBody
		if min > s.min {
			min = s.min
		}
		parts = append(parts, s.compile(false, min).String())
	}
	return regexp.MustCompile(strings.Join(parts, "|"))
}

// BenignLogins are the usernames that carry no secret at all.
//
// `git` is the SSH login of every git host on earth and appeared in 55 of the
// URLs the too-broad detector flagged. The other three are the conventional
// placeholders GitHub and GitLab document for token authentication: there, the
// name says "the password is the credential" — so a URL using one of them WITH
// a password half is a leak, and the secret is the password.
var BenignLogins = []string{"git", "oauth2", "x-access-token", "token", "gitlab-ci-token"}

func benign(user string) bool {
	for _, b := range BenignLogins {
		if user == b {
			return true
		}
	}
	return false
}

// Finding is what [Inspect] concluded. The zero value means a URL with no
// userinfo, which is the ordinary case.
//
// Nothing in it holds the credential. It is built to be printed.
type Finding struct {
	// Verdict is the answer.
	Verdict Verdict
	// Host is the host the URL points at, without any port.
	Host string
	// Login is the username, and ONLY when that username is one of
	// [BenignLogins]. Any other name is left out rather than echoed.
	Login string
	// Shape names the credential format, e.g. "GitHub classic personal access
	// token", or describes the fallback that matched.
	Shape string
	// Prefix is the issuer's marker from the table, "" for a shape recognised
	// without one.
	Prefix string
	// Length is how many characters the credential has.
	Length int
	// Digest is the first 12 hex digits of the SHA-256 of the credential, so
	// that two findings can be told apart, the same credential can be
	// recognised in two places, and a person can confirm a revocation covered
	// the right value — none of which needs the value itself.
	Digest string
	// Half says where in the userinfo the credential was: "username" or
	// "password".
	Half string
	// Scp is true for a `git@host:path` URL, which has no scheme. It is
	// reported because such a URL cannot be rewritten by dropping the
	// userinfo — see [Strip].
	Scp bool
}

// Leak reports whether this finding is a credential that must be dealt with.
func (f Finding) Leak() bool { return f.Verdict == Secret }

// String describes the finding without disclosing it.
func (f Finding) String() string {
	switch f.Verdict {
	case Secret:
		b := &strings.Builder{}
		b.WriteString(f.Shape)
		if f.Prefix != "" {
			b.WriteString(" (" + f.Prefix + "…)")
		}
		b.WriteString(", " + itoa(f.Length) + " chars, sha256:" + f.Digest)
		b.WriteString(", in the " + f.Half + " of a URL for " + f.Host)
		return b.String()
	case Username:
		if f.Login != "" {
			return "username " + f.Login + "@" + f.Host
		}
		return "a username (not repeated) @" + f.Host
	default:
		return "no userinfo"
	}
}

// Inspect judges one URL.
//
// It accepts anything a git remote can be: an https URL, an ssh URL, the
// scp-style `git@host:path`, a local path, a config line that happens to
// contain a URL. Anything it cannot read as carrying userinfo is reported as
// [NoUserinfo] rather than guessed at — this decides whether to refuse an
// action, and a refusal from a misread line is worse than no guard.
func Inspect(raw string) Finding {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		return inspectAuthority(s[i+len("://"):])
	}
	// scp-style: [user@]host:path. No scheme, and the colon separates host
	// from path rather than user from password.
	at := strings.IndexByte(s, '@')
	if at <= 0 || strings.ContainsAny(s[:at], "/ ") {
		return Finding{} // a local path, a bare host, an ordinary word
	}
	rest := s[at+1:]
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return Finding{} // no path separator: not a git remote at all
	}
	f := inspectUserinfo(s[:at], hostOf(rest[:colon]))
	f.Scp = true
	if f.Verdict == Secret && f.Shape == genericName {
		// A username is what an scp-style userinfo IS, by construction: it is
		// handed to ssh as a login. So the entropy fallback does not get to
		// call one a secret — only a KNOWN issuer prefix does, because a
		// string that starts with ghp_ is not somebody's login whatever
		// position it is in.
		return Finding{Verdict: Username, Host: f.Host, Scp: true}
	}
	return f
}

// inspectAuthority judges what follows "://".
func inspectAuthority(rest string) Finding {
	authority := rest
	if i := strings.IndexAny(authority, "/?#"); i >= 0 {
		authority = authority[:i]
	}
	// The LAST @ separates userinfo from host: a password may legally contain
	// one, a host may not.
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return Finding{Host: hostOf(authority)}
	}
	return inspectUserinfo(authority[:at], hostOf(authority[at+1:]))
}

// hostOf drops a port and lowercases, so that two spellings of one host are one
// host in a report.
func hostOf(hostport string) string {
	h := hostport
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i+1:], "]") {
		h = h[:i]
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

// oauthPassword is the placeholder GitHub documents alongside a token in the
// USERNAME half. It is not a secret, and reporting it as one would name the
// wrong half and digest the wrong value.
const oauthPassword = "x-oauth-basic"

// inspectUserinfo is the decision, in order.
//
// The order is what makes the two real failures impossible. A known shape is
// looked for in BOTH halves before anything else, so `TOKEN@host` with no
// username is caught (failure one) and `oauth2:TOKEN@host` is reported as a
// password rather than as a suspicious login. Only then does a password half
// count on its own, and only then does a plain name read as a name (failure
// two).
func inspectUserinfo(userinfo, host string) Finding {
	user, pass, hasPass := strings.Cut(userinfo, ":")

	if s, ok := match(user); ok {
		return secret(s, user, "username", host)
	}
	if hasPass {
		if s, ok := match(pass); ok {
			return secret(s, pass, "password", host)
		}
	}
	if Generic(user) {
		return secret(Shape{Name: genericName}, user, "username", host)
	}
	if hasPass && Generic(pass) {
		return secret(Shape{Name: genericName}, pass, "password", host)
	}
	// A password half that is present and is not a documented placeholder is a
	// credential whatever it looks like: `https://alice:hunter2@host` is a
	// disclosed password even though "hunter2" has no shape at all. There is no
	// legitimate reason for that half to exist in a remote URL.
	if hasPass && pass != "" && pass != oauthPassword {
		return secret(Shape{Name: "a password embedded in a URL"}, pass, "password", host)
	}
	f := Finding{Verdict: Username, Host: host}
	if benign(user) {
		f.Login = user
	}
	return f
}

func secret(s Shape, value, half, host string) Finding {
	sum := sha256.Sum256([]byte(value))
	return Finding{
		Verdict: Secret,
		Host:    host,
		Shape:   s.Name,
		Prefix:  s.Prefix,
		Length:  len(value),
		Digest:  hex.EncodeToString(sum[:])[:12],
		Half:    half,
	}
}

// match reports which known shape a userinfo half is, if any.
func match(half string) (Shape, bool) {
	for i, re := range anchoredShapes {
		if re.MatchString(half) {
			return shapes[i], true
		}
	}
	return Shape{}, false
}

const genericName = "a high-entropy value, from no issuer this knows"

// Generic is the fallback: does this look like a credential from an issuer
// whose prefix is not in the table?
//
// The thresholds are set against the thing that actually goes wrong. They must
// not fire on a login — a username is what 55 of the 57 flagged URLs held — so
// they ask for length, for all three of digits, upper and lower case, for a
// charset with no `.` or `%` in it (an address, or percent-encoding, is a
// name), and for per-character entropy above what a word has.
//
// # What this deliberately does not catch
//
// An unprefixed credential SHORTER than 24 characters, and one that is all one
// case without digits. Both are reachable; both are also the shape of a login,
// and a guard that refuses logins is a guard that gets removed. The known
// prefixes are what cover those cases, and adding an issuer to [Shapes] is the
// way to cover one more.
func Generic(s string) bool {
	const minLen = 24
	if len(s) < minLen {
		return false
	}
	var upper, lower, digit, hexOnly = false, false, false, true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		case c == '_' || c == '-' || c == '+' || c == '/' || c == '=':
		default:
			// A dot, an @, a percent, a space, anything non-ASCII: this is a
			// name or an encoding, not a token body.
			return false
		}
		if !isHex(c) {
			hexOnly = false
		}
	}
	if entropy(s) < 3.2 {
		return false
	}
	// A hex digest is all one case by nature, so the mixed-case requirement
	// would miss every 40-character hex token. Length stands in for it.
	if hexOnly && len(s) >= 32 {
		return true
	}
	return upper && lower && digit
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// entropy is Shannon entropy in bits per character. It stands in for "does not
// look like a word": English prose sits near 2 bits, a random base64 string
// near 6.
func entropy(s string) float64 {
	var freq [256]int
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range freq {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// Strip removes a credential from a URL, leaving the URL git should have had:
// `https://host/owner/repo`, with no userinfo, so that git falls back to the
// configured credential helper.
//
// It returns changed=false and the input untouched when there is nothing to
// strip — a clean URL, or userinfo that is a username. Calling it twice is the
// same as calling it once, which is what lets a repair run be re-run safely
// over a machine that is already half repaired.
//
// An scp-style URL is NOT rewritten even when its userinfo is a credential:
// dropping the user from `ghp_…@host:owner/repo` leaves `host:owner/repo`,
// which git reads as a relative path in some versions and a remote in others.
// Such a finding is reported for a person to fix, because a repair that might
// mean two things is not a repair.
func Strip(raw string) (string, bool) {
	f := Inspect(raw)
	if !f.Leak() || f.Scp {
		return raw, false
	}
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw, false
	}
	head, rest := raw[:i+len("://")], raw[i+len("://"):]
	end := len(rest)
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		end = j
	}
	at := strings.LastIndexByte(rest[:end], '@')
	if at < 0 {
		return raw, false
	}
	out := head + rest[at+1:]
	// Read the result back rather than trusting the arithmetic. A repair that
	// reports success without looking is how 117 of these went unnoticed in the
	// first place.
	if Inspect(out).Leak() {
		return raw, false
	}
	return out, true
}
