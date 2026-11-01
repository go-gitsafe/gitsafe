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

// resetAt renders the moment GitHub's hourly window reopens, and only when the
// headers say the budget is actually gone.
//
// ⚠ `X-RateLimit-Reset` RIDES ON ALMOST EVERY ANSWER, spent or not, so a reset
// on its own is not a deadline -- it is `remaining: 0` beside it that makes one.
// Reading the reset alone would attach a confident clock time to a refusal that
// has nothing to do with the budget.
func resetAt(header http.Header) string {
	if header.Get("X-RateLimit-Remaining") != "0" {
		return ""
	}
	s := header.Get("X-RateLimit-Reset")
	if s == "" {
		return ""
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(n, 0).Format(time.TimeOnly)
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
//	403 with no budget left          a limit it CANNOT name: hourly or burst,
//	                                 with the resource and the reset it saw
//	anything else                    the status, and GitHub's own sentence
//
// ⛔ The fourth line used to say "the hourly budget". It cannot know that: a
// burst refusal carries remaining==0 too, and on 2026-11-01 two sessions were
// sent to wait on that clock while the core budget was 96% unspent. A tool that
// names a cause is believed, so it may only name one it can establish.
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
		// ⛔ A BURST REFUSAL CAN ALSO CARRY remaining=0, and then a minute is
		// NOT the documented wait. GitHub's rules are ordered -- Retry-After,
		// then remaining==0, then "at least one minute" -- and under-waiting is
		// not free: "Continuing to make requests while you are rate limited may
		// result in the banning of your integration."
		if at := resetAt(header); at != "" {
			return fmt.Errorf("GitHub's burst limit (%d): too many writes too "+
				"quickly. Nothing is safe before %s; the credential is fine",
				status, at)
		}
		return fmt.Errorf("GitHub's burst limit (%d): too many writes too "+
			"quickly. Wait a minute and retry; the credential is fine", status)

	case header.Get("X-RateLimit-Remaining") == "0":
		// ⛔⛔ remaining==0 DOES NOT SAY WHICH LIMIT REFUSED, so this no longer
		// claims it does. Measured by two sessions on 2026-11-01, independently,
		// on the same minute: a merge answered 403 with `API rate limit
		// exceeded` -- no "secondary" wording, no Retry-After, so it lands here
		// -- while `gh api rate_limit` reported the core budget 96% UNSPENT
		// (4827 of 5000) and `gh pr create` went through twice. The hourly
		// budget was plainly not spent, and this said it was.
		//
		// GitHub's own documentation settles it: for SECONDARY limit errors it
		// says "if x-ratelimit-remaining is 0, wait until x-ratelimit-reset,
		// otherwise wait at least one minute". That advice would be pointless if
		// a secondary refusal could not carry remaining==0 -- so it can, and
		// this header cannot tell the two apart.
		//
		// ⭐ WHAT IT CAN SAY is what it observed, plus the one-call measurement
		// that settles it. "The hourly budget resets at 10:03" is actionable and
		// was wrong twice in one morning: both sessions waited on a clock that
		// was not the one running out. Naming the resource matters too -- the
		// budget is per resource family, so a drained one says nothing about the
		// rest.
		what := "its rate limit"
		if r := header.Get("X-RateLimit-Resource"); r != "" {
			what = "the " + r + " budget"
		}
		if at := resetAt(header); at != "" {
			return fmt.Errorf("GitHub refused this (%d) reporting %s exhausted "+
				"until %s. That may be the hourly budget or a burst limit, which "+
				"answers the same way: if another call succeeds now, it is the "+
				"burst one. The credential is fine", status, what, at)
		}
		return fmt.Errorf("GitHub refused this (%d) reporting %s exhausted. That "+
			"may be the hourly budget or a burst limit, which answers the same "+
			"way: if another call succeeds now, it is the burst one. The "+
			"credential is fine", status, what)
	}
	if msg.Message != "" {
		return fmt.Errorf("GitHub answered %d: %s", status, msg.Message)
	}
	return fmt.Errorf("GitHub answered %d", status)
}

// RetryAt says WHEN a refused request may be sent again, and whether sending it
// again can help at all.
//
// WhyRefused answers "what happened"; this answers "what now". They are
// separate because the second one has an order of its own, taken from GitHub's
// "Rate limits for the REST API" and not from the first one's switch:
//
//	Retry-After present              that many seconds; GitHub's own number
//	remaining == 0, with a reset      that reset; the window is shut until then
//	"secondary rate limit", "abuse"  at least one minute, says the documentation
//	a dead credential                never: waiting cannot revive a token
//	anything else                    unknown, so this does not guess
//
// ⛔ THE ORDER DIFFERS FROM WhyRefused ON PURPOSE. A burst refusal can carry
// `remaining: 0` as well, and when it does, a minute is the WRONG wait — the
// documentation says "if x-ratelimit-remaining is 0, wait until
// x-ratelimit-reset, otherwise wait at least one minute". WhyRefused puts the
// burst wording first because it is the better NAME; this puts the longer wait
// first because under-waiting is the one that costs: "Continuing to make
// requests while you are rate limited may result in the banning of your
// integration."
//
// ⚠ now is a parameter, not a call to time.Now, because the only interesting
// cases are about a clock and a test must be able to set it.
func RetryAt(status int, header http.Header, body []byte, now time.Time) (time.Time, bool) {
	var msg struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &msg)
	lower := strings.ToLower(msg.Message)

	// A credential GitHub will not accept is not a wait. Answering "in sixty
	// seconds" here would send a caller into a loop that cannot end in a
	// success, which is worse than refusing at once.
	if status == http.StatusUnauthorized || strings.Contains(lower, "bad credentials") {
		return time.Time{}, false
	}
	if s := header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return now.Add(time.Duration(n) * time.Second), true
		}
	}
	if header.Get("X-RateLimit-Remaining") == "0" {
		if s := header.Get("X-RateLimit-Reset"); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return time.Unix(n, 0), true
			}
		}
	}
	if strings.Contains(lower, "secondary rate limit") || strings.Contains(lower, "abuse detection") {
		return now.Add(time.Minute), true
	}
	return time.Time{}, false
}

// Refusal is a refused answer with both halves kept together: the sentence a
// person reads, and the instant a program may try again.
//
// It exists so a caller can wait without parsing a message. The sentence is the
// one WhyRefused writes, unchanged, because it is what every existing test and
// every reader already knows.
type Refusal struct {
	// At is when a retry may help, and Again says whether there is any point.
	At    time.Time
	Again bool
	// Wait is how long to wait, measured from the instant handed to Refused
	// rather than read off this machine's clock. Never negative.
	//
	// ⛔ SLEEP FOR THIS, DO NOT SUBTRACT At FROM time.Now(). x-ratelimit-reset
	// is an absolute UTC epoch second, so At is right whatever this host
	// believes the time is -- but `time.Until(At)` is only right if the host's
	// clock agrees with GitHub's, and a 403 here was once read as a wait of
	// over an hour because it did not. Pass Refused the `Date` header's instant
	// (see Now) and this field is the true remaining time, skew and all.
	Wait time.Duration
	// Err is WhyRefused's sentence. Error and Unwrap delegate to it, so a
	// Refusal reads and compares exactly as that error did.
	Err error
}

// Error returns WhyRefused's sentence, so wrapping loses nothing.
func (r *Refusal) Error() string { return r.Err.Error() }

// Unwrap exposes the sentence to errors.Is and errors.As.
func (r *Refusal) Unwrap() error { return r.Err }

// Refused builds the Refusal for an answer that is not a success: WhyRefused's
// sentence and RetryAt's instant, from one reading of the same headers.
func Refused(status int, header http.Header, body []byte, now time.Time) *Refusal {
	at, again := RetryAt(status, header, body, now)
	r := &Refusal{At: at, Again: again, Err: WhyRefused(status, header, body)}
	if again {
		if d := at.Sub(now); d > 0 {
			r.Wait = d
		}
	}
	return r
}

// Now is the instant GitHub itself was at when it wrote this answer, taken from
// the `Date` header, falling back to local when there is no usable one.
//
// It exists so a wait can be measured against the clock that set the deadline.
// RFC 9110 §6.6.1 requires an origin server to send Date, and GitHub does; a
// proxy that strips it leaves local, which is the behaviour this had before.
func Now(header http.Header, local time.Time) time.Time {
	if t, err := http.ParseTime(header.Get("Date")); err == nil {
		return t
	}
	return local
}
