package ghauth

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAnEmptyTokenFile(t *testing.T) {
	// An empty file is not a token, and saying so beats asking GitHub who
	// nobody is.
	p := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(p, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(p); err == nil {
		t.Error("an empty file was accepted as a token")
	}
}

func TestATokenIsReadWithoutItsWhitespace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("  abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc" {
		t.Errorf("read %q", got)
	}
}

// GitHub's scopes are a HIERARCHY, not a list. A token ticked `write:packages`
// can read packages, and its X-OAuth-Scopes header says only `write:packages`.
//
// This is the reading that cost a wrong diagnosis: a token carrying
// `delete:packages, write:packages` was reported as missing `read:packages`,
// the reading was believed, and the API call that failed was blamed on the
// scope rather than on the token it actually used. A guard that cries wolf
// gets read as a fact.
func TestScopesAreAHierarchy(t *testing.T) {
	for _, tc := range []struct {
		name string
		have []string
		want []string
		gone []string // what must still be reported missing
	}{
		{"write:packages covers read", []string{"delete:packages", "write:packages"}, []string{"read:packages"}, nil},
		{"delete does not come free", []string{"write:packages"}, []string{"delete:packages"}, []string{"delete:packages"}},
		{"admin:org covers read:org", []string{"admin:org"}, []string{"read:org", "write:org"}, nil},
		{"repo covers public_repo", []string{"repo"}, []string{"public_repo", "repo:status"}, nil},
		{"user covers user:email", []string{"user"}, []string{"user:email"}, nil},
		{"a scope nothing contains", []string{"gist"}, []string{"gist"}, nil},
		{"and one that is simply absent", []string{"gist"}, []string{"workflow"}, []string{"workflow"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Missing(tc.have, tc.want)
			if len(got) != len(tc.gone) {
				t.Fatalf("Missing(%v, %v) = %v, want %v", tc.have, tc.want, got, tc.gone)
			}
			for i := range got {
				if got[i] != tc.gone[i] {
					t.Errorf("missingFrom = %v, want %v", got, tc.gone)
				}
			}
		})
	}
}

// Expansion is transitive and terminates: admin:public_key contains
// write:public_key, which contains read:public_key.
func TestScopeExpansionIsTransitive(t *testing.T) {
	set := Expand([]string{"admin:public_key"})
	for _, s := range []string{"admin:public_key", "write:public_key", "read:public_key"} {
		if !set[s] {
			t.Errorf("%s is not in the expansion: %v", s, set)
		}
	}
}

// ⛔⛔ A 403 IS FOUR DIFFERENT SITUATIONS AND ONLY ONE OF THEM IS A DEAD TOKEN.
// ghscopes and ghpkg both said "the token is probably expired or revoked" for
// every non-success, and that sentence sends somebody to revoke and reissue a
// credential that was working.
//
// ⭐ IT HAPPENED. After seven pull requests merged in quick succession, ghmerge
// and then ghscopes both answered 403 while `gitpush --dry-run` on the same
// token answered "Everything up-to-date" -- git authenticating with it one
// second, the API refusing it the next. GitHub's burst limit. The measurement
// said the credential was alive, the label said it was dead, and the label won.
func TestWhyRefusedNamesWhichRefusalItWas(t *testing.T) {
	t.Parallel()

	// The reset is rendered in local time, so the expectation is computed the
	// same way instead of written as a literal clock: a hard-coded "14:13:20"
	// passes only in the zone it was written in, and CI does not run in that
	// zone.
	resetClock := time.Unix(1760000000, 0).Format(time.TimeOnly)

	for _, c := range []struct {
		name   string
		status int
		header http.Header
		body   string
		// want is a phrase the sentence must contain, and notWant one it must
		// NOT: naming the burst limit is only useful if it stops claiming the
		// credential is gone.
		want, notWant string
	}{
		{
			name:   "the burst limit, which GitHub names in the body",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   `{"message":"You have exceeded a secondary rate limit."}`,
			want:   "burst limit", notWant: "expired",
		},
		{
			name:   "the burst limit under its older name",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   `{"message":"abuse detection mechanism triggered"}`,
			want:   "burst limit", notWant: "expired",
		},
		{
			// ⭐ AND THE WAIT IS GITHUB'S OWN NUMBER, so a person does not guess.
			name:   "the burst limit with a wait attached",
			status: http.StatusForbidden,
			header: http.Header{"Retry-After": []string{"47"}},
			body:   `{"message":"slow down"}`,
			want:   "47", notWant: "expired",
		},
		{
			name:   "a Retry-After with nothing else, which is still the burst",
			status: http.StatusTooManyRequests,
			header: http.Header{"Retry-After": []string{"60"}},
			body:   `{}`,
			want:   "burst limit", notWant: "expired",
		},
		{
			// ⛔⛔ THIS USED TO ASSERT "hourly budget", AND THAT WAS WRONG TWICE
			// IN ONE MORNING. Exactly this shape -- 403, `API rate limit
			// exceeded`, no secondary wording, no Retry-After, remaining 0 --
			// reached two sessions on 2026-11-01 while `rate_limit` reported the
			// core budget 96% unspent and `pr create` worked. remaining==0
			// cannot tell the hourly budget from a burst limit, because GitHub's
			// own rules for SECONDARY errors branch on that very header. So the
			// message may report what it saw and must not name the cause.
			name:   "no budget left, and which limit is unknowable from it",
			status: http.StatusForbidden,
			header: http.Header{"X-Ratelimit-Remaining": []string{"0"},
				"X-Ratelimit-Reset": []string{"1760000000"}},
			body: `{"message":"API rate limit exceeded"}`,
			want: "may be the hourly budget or a burst limit", notWant: "expired",
		},
		{
			// The budget is per RESOURCE family, so a drained one says nothing
			// about the rest -- and a reader who knows which one was drained can
			// tell "my sweep spent core" from "something else is throttled".
			name:   "it names the resource GitHub blamed",
			status: http.StatusForbidden,
			header: http.Header{"X-Ratelimit-Remaining": []string{"0"},
				"X-Ratelimit-Resource": []string{"core"},
				"X-Ratelimit-Reset":    []string{"1760000000"}},
			body: `{"message":"API rate limit exceeded"}`,
			want: "the core budget", notWant: "expired",
		},
		{
			// ⛔⛔ THE PRECEDENCE, WHICH COST TWO SESSIONS AN HOUR EACH. These two
			// branches are one `case` apart and a refusal can satisfy BOTH, so
			// which one wins is a decision -- and GitHub decided it: the
			// documented rules read Retry-After, then remaining==0, so the burst
			// signal is tested first. Reorder them and every burst refusal starts
			// claiming the hourly budget is spent, which sends the next person to
			// wait fifty minutes for a limit that lifts in one. Nothing pinned
			// this before; a reordering would have passed the whole suite.
			name:   "a burst refusal that also shows an empty budget",
			status: http.StatusForbidden,
			header: http.Header{"X-Ratelimit-Remaining": []string{"0"},
				"X-Ratelimit-Reset": []string{"1760000000"}},
			body: `{"message":"You have exceeded a secondary rate limit."}`,
			want: "burst limit", notWant: "hourly budget",
		},
		{
			// ⭐ AND THEN THE WAIT IS THE RESET, NOT A MINUTE. GitHub's second
			// rule for a burst refusal is "if remaining is 0, wait until reset",
			// and the minute below is only the third. Under-waiting is not free:
			// continuing to call while limited risks the integration being
			// banned, so the sentence has to carry the deadline it knows.
			name:   "a burst refusal whose only stated wait is the reset",
			status: http.StatusForbidden,
			header: http.Header{"X-Ratelimit-Remaining": []string{"0"},
				"X-Ratelimit-Reset": []string{"1760000000"}},
			body: `{"message":"You have exceeded a secondary rate limit."}`,
			want: resetClock, notWant: "Wait a minute",
		},
		{
			// Retry-After is GitHub's first rule and outranks the reset.
			name:   "a wait GitHub states outranks a budget it shows as empty",
			status: http.StatusForbidden,
			header: http.Header{"Retry-After": []string{"47"},
				"X-Ratelimit-Remaining": []string{"0"},
				"X-Ratelimit-Reset":     []string{"1760000000"}},
			body: `{"message":"You have exceeded a secondary rate limit."}`,
			want: "47", notWant: "hourly budget",
		},
		{
			// ⚠ A RESET WITHOUT AN EMPTY BUDGET IS NOT A DEADLINE, and this is
			// the only shape that proves it: a burst refusal whose hourly budget
			// is barely touched. `X-RateLimit-Reset` rides on nearly every
			// answer, so reading it without checking `remaining` pins a
			// confident clock time -- a fifty-minute wait -- onto a limit that
			// lifts in one minute.
			//
			// ⛔ MY FIRST VERSION OF THIS CASE PROVED NOTHING. It used a
			// scope refusal, where neither branch ever calls resetAt, so
			// deleting the guard it was written for left the suite green. A test
			// has to reach the code it is about.
			name:   "a burst refusal whose hourly budget is NOT spent",
			status: http.StatusForbidden,
			header: http.Header{"X-Ratelimit-Remaining": []string{"4312"},
				"X-Ratelimit-Reset": []string{"1760000000"}},
			body: `{"message":"You have exceeded a secondary rate limit."}`,
			want: "Wait a minute", notWant: resetClock,
		},
		{
			// ⛔ THE ONE CASE THE OLD SENTENCE WAS RIGHT ABOUT, which has to keep
			// saying so or this change trades one wrong diagnosis for another.
			name:   "a credential GitHub will not accept",
			status: http.StatusUnauthorized,
			header: http.Header{},
			body:   `{"message":"Bad credentials"}`,
			want:   "expired", notWant: "burst",
		},
		{
			name:   "bad credentials behind a 403",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   `{"message":"Bad credentials"}`,
			want:   "expired", notWant: "burst",
		},
		{
			// Anything else quotes GitHub rather than inventing a cause.
			name:   "a refusal with its own reason",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   `{"message":"Resource not accessible by personal access token"}`,
			want:   "not accessible by personal access token", notWant: "expired",
		},
		{
			name:   "a refusal with nothing to say",
			status: http.StatusForbidden,
			header: http.Header{},
			body:   ``,
			want:   "403", notWant: "expired",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := WhyRefused(c.status, c.header, []byte(c.body))
			if err == nil {
				t.Fatal("WhyRefused said nothing about a refusal")
			}
			got := err.Error()
			if !strings.Contains(got, c.want) {
				t.Errorf("WhyRefused said %q, want it to mention %q", got, c.want)
			}
			if c.notWant != "" && strings.Contains(got, c.notWant) {
				t.Errorf("WhyRefused said %q, which still blames %q", got, c.notWant)
			}
			// ⛔ AND NO ANSWER MAY QUOTE AN AUTHORIZATION HEADER, however a caller
			// builds one. The inputs here are a status, response headers and
			// GitHub's own body; none is the request's own credential, and this
			// asserts the boundary rather than trusting it.
			if strings.Contains(strings.ToLower(got), "bearer ") {
				t.Errorf("WhyRefused said %q, which carries an authorization header", got)
			}
		})
	}
}

// ⛔ THE CASE THIS WAS WRITTEN FOR: a refusal that carries BOTH the burst
// wording and remaining==0. GitHub's documentation says so in as many words --
// "if x-ratelimit-remaining is 0, wait until x-ratelimit-reset, otherwise wait
// at least one minute" -- and a minute is then far too short. On 2026-11-01 two
// green pull requests waited on exactly this answer.
//
// ⚠ WhyRefused and RetryAt deliberately order the two signals differently, so
// this suite has to pin the SECOND order as well: naming prefers the burst
// wording, waiting prefers the longer clock.
func TestWhenARefusalMayBeRetried(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 10, 53, 30, 0, time.UTC)
	reset := now.Add(10*time.Minute + 15*time.Second)

	header := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}

	for _, c := range []struct {
		name   string
		status int
		header http.Header
		body   string
		at     time.Time // zero means "no retry"
		again  bool
	}{
		{
			name:   "Retry-After is GitHub's own number and wins",
			status: http.StatusForbidden,
			header: header("Retry-After", "42",
				"X-RateLimit-Remaining", "0",
				"X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10)),
			body:  `{"message":"You have exceeded a secondary rate limit"}`,
			at:    now.Add(42 * time.Second),
			again: true,
		},
		{
			name:   "burst wording WITH remaining 0 waits for the reset, not a minute",
			status: http.StatusForbidden,
			header: header("X-RateLimit-Remaining", "0",
				"X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10)),
			body:  `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`,
			at:    reset,
			again: true,
		},
		{
			name:   "burst wording alone is the documented minute",
			status: http.StatusForbidden,
			header: header("X-RateLimit-Remaining", "4827"),
			body:   `{"message":"You have exceeded a secondary rate limit"}`,
			at:     now.Add(time.Minute),
			again:  true,
		},
		{
			name:   "a spent budget waits for its reset",
			status: http.StatusForbidden,
			header: header("X-RateLimit-Remaining", "0",
				"X-RateLimit-Resource", "core",
				"X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10)),
			body:  `{"message":"API rate limit exceeded for user ID 11405852."}`,
			at:    reset,
			again: true,
		},
		{
			// Waiting cannot revive a token, and answering "in a minute"
			// would build a loop with no success in it.
			name:   "a dead credential is never a wait",
			status: http.StatusUnauthorized,
			header: header("Retry-After", "60"),
			body:   `{"message":"Bad credentials"}`,
		},
		{
			name:   "a 404 says nothing about when",
			status: http.StatusNotFound,
			header: header(),
			body:   `{"message":"Not Found"}`,
		},
		{
			// remaining==0 rides on answers that have a reset but no number
			// this can parse; guessing a clock would be worse than saying so.
			name:   "an unreadable reset is not a deadline",
			status: http.StatusForbidden,
			header: header("X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "soon"),
			body:   `{"message":"API rate limit exceeded"}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			at, again := RetryAt(c.status, c.header, []byte(c.body), now)
			if again != c.again {
				t.Fatalf("RetryAt said again=%v, want %v (at %v)", again, c.again, at)
			}
			if !again {
				return
			}
			if !at.Equal(c.at) {
				t.Errorf("RetryAt said %v, want %v — %v too %s",
					at, c.at, c.at.Sub(at).Abs(), map[bool]string{true: "early", false: "late"}[at.Before(c.at)])
			}

			// Refused must carry BOTH halves from one reading of the same
			// headers: the sentence a person reads and the instant a program
			// waits for. A Refusal whose message differed from WhyRefused's
			// would quietly fork the vocabulary.
			r := Refused(c.status, c.header, []byte(c.body), now)
			if !r.At.Equal(at) || r.Again != again {
				t.Errorf("Refused said (%v, %v), RetryAt said (%v, %v)", r.At, r.Again, at, again)
			}
			if want := WhyRefused(c.status, c.header, []byte(c.body)).Error(); r.Error() != want {
				t.Errorf("Refusal reads %q, WhyRefused reads %q", r.Error(), want)
			}
			// Wait is what a caller sleeps for, so it has to be the distance
			// from the instant handed in -- not from this host's clock.
			if want := c.at.Sub(now); r.Wait != want {
				t.Errorf("Refusal says wait %v, want %v", r.Wait, want)
			}
		})
	}
}

// TestAWaitIsMeasuredAgainstGitHubsClockNotThisHost is the test for the defect
// that sent this work off course once: a 403 whose reset read as an hour away
// because the two clocks disagreed, so the wait was quoted from the wrong one.
//
// x-ratelimit-reset is an absolute UTC epoch second. The INSTANT is therefore
// always right; only the DURATION can be wrong, and only if it is measured
// locally. So the fixture gives the host a clock five minutes behind GitHub's
// and asserts the duration is GitHub's.
func TestAWaitIsMeasuredAgainstGitHubsClockNotThisHost(t *testing.T) {
	t.Parallel()

	github := time.Date(2026, 11, 1, 10, 53, 30, 0, time.UTC)
	reset := github.Add(7 * time.Minute)
	local := github.Add(-5 * time.Minute) // this host is slow

	h := http.Header{}
	h.Set("Date", github.Format(http.TimeFormat))
	h.Set("X-RateLimit-Remaining", "0")
	h.Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	body := []byte(`{"message":"You have exceeded a secondary rate limit"}`)

	if got := Now(h, local); !got.Equal(github) {
		t.Fatalf("Now said %v, want GitHub's %v", got, github)
	}
	if got := Refused(403, h, body, Now(h, local)).Wait; got != 7*time.Minute {
		t.Errorf("waiting %v; GitHub said seven minutes", got)
	}
	// ⛔ The control: measured locally it is twelve, which is the wrong answer
	// this field exists to stop anyone reaching. Without this line the test
	// above would also pass if Wait ignored its argument entirely.
	if got := Refused(403, h, body, local).Wait; got != 12*time.Minute {
		t.Fatalf("the fixture does not reproduce the skew: %v, want 12m", got)
	}
}

func TestAnAnswerWithNoUsableDateLeavesTheLocalClockAlone(t *testing.T) {
	t.Parallel()

	local := time.Date(2026, 11, 1, 10, 53, 30, 0, time.UTC)
	for _, date := range []string{"", "the first of November"} {
		h := http.Header{}
		if date != "" {
			h.Set("Date", date)
		}
		if got := Now(h, local); !got.Equal(local) {
			t.Errorf("Date %q gave %v, want the local %v", date, got, local)
		}
	}
}

func TestAResetAlreadyPastIsNotANegativeWait(t *testing.T) {
	t.Parallel()

	// A reset in the past is ordinary: the answer sat in a queue, or the two
	// clocks differ the other way. A negative duration passed to time.Sleep
	// returns at once, which is the right behaviour -- but a negative number
	// printed in a message reads as a defect, and `Wait > budget` comparisons
	// in callers would silently invert.
	now := time.Date(2026, 11, 1, 10, 53, 30, 0, time.UTC)
	h := http.Header{}
	h.Set("X-RateLimit-Remaining", "0")
	h.Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(-time.Hour).Unix(), 10))

	r := Refused(403, h, []byte(`{"message":"API rate limit exceeded"}`), now)
	if !r.Again {
		t.Fatal("a past reset still names a retry")
	}
	if r.Wait != 0 {
		t.Errorf("wait %v, want zero", r.Wait)
	}
}

func TestARefusalThatIsNotAWaitHasNoWait(t *testing.T) {
	t.Parallel()

	r := Refused(404, http.Header{}, []byte(`{"message":"Not Found"}`), time.Now())
	if r.Again || r.Wait != 0 {
		t.Errorf("a 404 says (again=%v, wait=%v), want (false, 0)", r.Again, r.Wait)
	}
}
