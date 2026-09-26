package credurl

import (
	"strings"
	"testing"
)

// tok builds a value that is SHAPED like a credential without being one.
//
// Assembled from pieces rather than written whole, and with a body that is
// visibly a pattern, so that nothing scanning this repository — a pre-commit
// hook, GitHub's own secret scanning, the guard in this very repository — finds
// something it must treat as live. What the tests need is the shape.
func tok(prefix string, n int) string {
	body := strings.Repeat("Ab3", n/3+1)[:n]
	return prefix + body
}

// TestTheTwoFailures is the reason this package exists. Two detectors were
// written by hand for one incident and both were wrong, in opposite directions:
// one missed a token with no username in front of it, the other called 55 SSH
// logins secrets.
//
// Every case asserts the verdict in BOTH directions. A table that only listed
// leaks would have passed for the too-broad detector, and a table that only
// listed clean URLs would have passed for a detector that never fires at all.
func TestTheTwoFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want Verdict
		host string
		half string
	}{
		// Failure one, the form that was caught.
		{"token as password", "https://user:" + tok("ghp_", 36) + "@github.com/o/r.git", Secret, "github.com", "password"},
		// Failure one, the form that was MISSED: no username half at all.
		// Five checkouts were carrying this and the detector said nothing.
		{"token as whole userinfo", "https://" + tok("ghp_", 36) + "@github.com/o/r.git", Secret, "github.com", "username"},
		// Failure two: 55 of these were reported as leaks. `git` is the SSH
		// login of every git host there is.
		{"ssh url", "ssh://git@github.com/o/r", Username, "github.com", ""},
		{"scp style", "git@plmlab.math.cnrs.fr:team/repo", Username, "plmlab.math.cnrs.fr", ""},
		// The documented token form: the username says "the password is the
		// credential", so the password is what is reported.
		{"gitlab oauth2", "https://oauth2:" + tok("glpat-", 20) + "@gitlab.com/o/r", Secret, "gitlab.com", "password"},
		{"clean https", "https://github.com/o/r", NoUserinfo, "github.com", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Inspect(tc.url)
			if got.Verdict != tc.want {
				t.Fatalf("verdict = %v, want %v (%s)", got.Verdict, tc.want, got)
			}
			if got.Host != tc.host {
				t.Errorf("host = %q, want %q", got.Host, tc.host)
			}
			if got.Half != tc.half {
				t.Errorf("half = %q, want %q", got.Half, tc.half)
			}
		})
	}
}

// TestNothingEchoesTheCredential is the rule from this machine's own
// configuration: verify a secret by its properties, never by printing it. A
// finding is built to be printed, so every field of it, and its String, must be
// free of the value.
func TestNothingEchoesTheCredential(t *testing.T) {
	const body = "Zq7Kp2Lm9Rt4Wx6Yn1Bv3Cd5" // the part that must never come back
	for _, u := range []string{
		"https://" + "ghp_" + body + "abcdefghij" + "@github.com/o/r.git",
		"https://x-access-token:" + "ghp_" + body + "abcdefghij" + "@github.com/o/r.git",
		"https://alice:" + body + "@example.org/o/r",
		"https://" + body + "AbC9@example.org/o/r",
	} {
		f := Inspect(u)
		if !f.Leak() {
			t.Fatalf("not reported as a leak: %s", u)
		}
		for _, field := range []string{f.String(), f.Shape, f.Prefix, f.Digest, f.Login, f.Host} {
			if strings.Contains(field, body) {
				t.Errorf("the credential reached a reported field: %q", field)
			}
		}
		if f.Length == 0 || f.Digest == "" {
			t.Errorf("a finding with no properties cannot be acted on: %+v", f)
		}
	}
}

// TestAUsernameIsNeverEchoedUnlessItIsKnownBenign: echoing a userinfo that this
// package decided was harmless would disclose it in exactly the case where the
// decision was wrong. So only names from the fixed list come back.
func TestAUsernameIsNeverEchoedUnlessItIsKnownBenign(t *testing.T) {
	if got := Inspect("ssh://git@github.com/o/r").Login; got != "git" {
		t.Errorf("Login = %q, want git", got)
	}
	f := Inspect("https://alice@example.org/o/r")
	if f.Verdict != Username {
		t.Fatalf("verdict = %v, want username", f.Verdict)
	}
	if f.Login != "" || strings.Contains(f.String(), "alice") {
		t.Errorf("an unknown username was echoed: %q / %s", f.Login, f)
	}
}

// TestEveryKnownShapeIsCaughtInBothHalves. A shape that is only checked in the
// password half is failure one again, one issuer at a time.
func TestEveryKnownShapeIsCaughtInBothHalves(t *testing.T) {
	for _, s := range Shapes() {
		v := tok(s.Prefix, 24)
		if s.Prefix == "AKIA" || s.Prefix == "ASIA" {
			v = s.Prefix + strings.Repeat("7ABCDE", 4)[:16]
		}
		for _, u := range []string{
			"https://" + v + "@host.example/o/r",
			"https://oauth2:" + v + "@host.example/o/r",
		} {
			f := Inspect(u)
			if !f.Leak() {
				t.Errorf("%s: not caught in %q", s.Name, strings.Replace(u, v, "<value>", 1))
				continue
			}
			if f.Shape != s.Name {
				t.Errorf("%s: reported as %q", s.Name, f.Shape)
			}
			if f.Prefix != s.Prefix {
				t.Errorf("%s: prefix = %q", s.Name, f.Prefix)
			}
		}
	}
}

// TestKnownPrefixWinsOverTheHalfItIsIn: GitHub documents `TOKEN:x-oauth-basic`.
// Reporting the password half there would digest a public placeholder and name
// the wrong half — a finding that identifies nothing.
func TestKnownPrefixWinsOverTheHalfItIsIn(t *testing.T) {
	f := Inspect("https://" + tok("ghp_", 36) + ":x-oauth-basic@github.com/o/r")
	if !f.Leak() || f.Half != "username" {
		t.Fatalf("got %v half=%q, want a secret in the username", f.Verdict, f.Half)
	}
	if f.Prefix != "ghp_" {
		t.Errorf("prefix = %q, want ghp_", f.Prefix)
	}
}

// TestBenignLoginsAloneAreNotLeaks. `https://x-access-token@github.com` appears
// in git's own error message when a password is not supplied; calling that a
// leak would refuse a push that has no secret in it anywhere.
func TestBenignLoginsAloneAreNotLeaks(t *testing.T) {
	for _, l := range BenignLogins {
		if f := Inspect("https://" + l + "@github.com/o/r"); f.Verdict != Username {
			t.Errorf("%s alone = %v, want username", l, f.Verdict)
		}
		// With a password half, the same name means the opposite.
		if f := Inspect("https://" + l + ":sBcD3fGhIjKlMnOpQrStUvWx@github.com/o/r"); !f.Leak() {
			t.Errorf("%s with a password = %v, want a leak", l, f.Verdict)
		}
	}
}

// TestAPasswordHalfIsACredentialWhateverItLooksLike: a weak password in a URL
// is still disclosed by every fetch. There is no shape test that would pass it
// and no legitimate reason for that half to exist.
func TestAPasswordHalfIsACredentialWhateverItLooksLike(t *testing.T) {
	f := Inspect("https://alice:hunter2@example.org/o/r")
	if !f.Leak() || f.Half != "password" {
		t.Fatalf("got %v half=%q", f.Verdict, f.Half)
	}
	if f.Prefix != "" {
		t.Errorf("prefix = %q, want empty for a shapeless password", f.Prefix)
	}
	// An empty password half is not a credential: `https://token:@host` carries
	// nothing.
	if f := Inspect("https://token:@github.com/o/r"); f.Leak() {
		t.Errorf("an empty password read as a leak: %s", f)
	}
}

// TestGenericFallback, in both directions. The false-positive half is the one
// that matters: every case below that must NOT fire is something a real remote
// URL has held on this machine.
func TestGenericFallback(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"Zq7Kp2Lm9Rt4Wx6Yn1Bv3Cd5Ef8", true},              // mixed case, digits, long
		{"deadbeefcafe0123456789abcdef0123456789ab", true}, // a 40-char hex digest: one case by nature
		{"git", false},                             // the SSH login, 55 times over
		{"oauth2", false},                          // a documented placeholder
		{"david_delavennat", false},                // a person
		{"first.last", false},                      // a dot says address, not token
		{"averyveryverylongusernameindeed", false}, // long, but no digits and one case
		{"MyPassword1234567890", false},            // 20 chars: under the floor, and stated as a gap
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaAb3", false}, // long and mixed but no entropy
		{strings.Repeat("x", 30) + "%20", false},   // percent-encoding is not a token body
	} {
		if got := Generic(tc.in); got != tc.want {
			t.Errorf("Generic(%d chars) = %v, want %v", len(tc.in), got, tc.want)
		}
	}
}

// TestScpUserinfoIsALoginUnlessItIsAnIssuersToken. In `git@host:path` the
// userinfo is handed to ssh as a login, so the entropy fallback has no business
// there — but a string beginning ghp_ is nobody's login.
func TestScpUserinfoIsALoginUnlessItIsAnIssuersToken(t *testing.T) {
	if f := Inspect("Zq7Kp2Lm9Rt4Wx6Yn1Bv3Cd5Ef8@github.com:o/r"); f.Verdict != Username {
		t.Errorf("a high-entropy ssh login = %v, want username", f.Verdict)
	}
	f := Inspect(tok("ghp_", 36) + "@github.com:o/r")
	if !f.Leak() || !f.Scp {
		t.Errorf("an issuer's token in scp form = %v scp=%v, want a leak", f.Verdict, f.Scp)
	}
	// And it is not rewritten: dropping the user leaves `host:path`, which is
	// ambiguous.
	if out, changed := Strip(tok("ghp_", 36) + "@github.com:o/r"); changed {
		t.Errorf("an scp URL was rewritten to %q", out)
	}
}

// TestNotAURLAtAll: everything here must be read as carrying no userinfo rather
// than guessed at. A scan walks a whole home directory, so it meets all of it.
func TestNotAURLAtAll(t *testing.T) {
	for _, in := range []string{
		"", "   ", "/home/me/repo", "../elsewhere", "origin",
		"file:///home/me/repo", "https://github.com", "https://github.com:443/o/r",
		"me@example.org", "C:\\Users\\me\\repo", "some sentence with an @ in it",
	} {
		if f := Inspect(in); f.Verdict == Secret {
			t.Errorf("Inspect(%q) = %s, want no leak", in, f)
		}
	}
}

func TestStrip(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		changed  bool
	}{
		{"https://" + tok("ghp_", 36) + "@github.com/o/r.git", "https://github.com/o/r.git", true},
		{"https://user:" + tok("ghp_", 36) + "@github.com/o/r.git", "https://github.com/o/r.git", true},
		{"https://oauth2:" + tok("glpat-", 20) + "@gitlab.com/o/r", "https://gitlab.com/o/r", true},
		{"https://" + tok("ghp_", 36) + "@github.com:8443/o/r.git", "https://github.com:8443/o/r.git", true},
		// Not touched: no credential to remove. Stripping `git` here would
		// break every ssh remote on the machine.
		{"ssh://git@github.com/o/r", "ssh://git@github.com/o/r", false},
		{"git@github.com:o/r", "git@github.com:o/r", false},
		{"https://github.com/o/r.git", "https://github.com/o/r.git", false},
		{"", "", false},
	} {
		got, changed := Strip(tc.in)
		if got != tc.want || changed != tc.changed {
			t.Errorf("Strip(…) = %q,%v want %q,%v", got, changed, tc.want, tc.changed)
		}
		// Idempotent: a repair run over a machine that is already half repaired
		// must not need to know which half.
		again, changedAgain := Strip(got)
		if again != got || changedAgain {
			t.Errorf("Strip is not idempotent: %q -> %q (%v)", got, again, changedAgain)
		}
	}
}

// TestTokenPatternAsksTheSameTable. Two lists of issuer prefixes in two
// packages is how one of them ends up missing the prefix that leaks: the
// redactor's list had no GitLab at all, and the command-line guard's had no
// ghu_, ghr_ or glpat-.
func TestTokenPatternAsksTheSameTable(t *testing.T) {
	re := TokenPattern(16)
	for _, s := range Shapes() {
		v := tok(s.Prefix, 24)
		if s.Prefix == "AKIA" || s.Prefix == "ASIA" {
			v = s.Prefix + strings.Repeat("7ABCDE", 4)[:16]
		}
		if !re.MatchString("blah " + v + " blah") {
			t.Errorf("%s (%s) is in the table but not in the pattern", s.Name, s.Prefix)
		}
	}
	if re.MatchString("an ordinary line of prose about a git push") {
		t.Error("the pattern matched prose")
	}
	// A caller that must not miss a truncated token may lower the floor, and
	// the prefixes stay the same.
	if !TokenPattern(8).MatchString("ghp_" + strings.Repeat("A", 8)) {
		t.Error("a lowered floor did not lower")
	}
	if TokenPattern(16).MatchString("ghp_" + strings.Repeat("A", 8)) {
		t.Error("the floor did not hold")
	}
}

func TestVerdictString(t *testing.T) {
	for v, want := range map[Verdict]string{NoUserinfo: "no userinfo", Username: "username", Secret: "secret"} {
		if got := v.String(); got != want {
			t.Errorf("Verdict(%d) = %q, want %q", v, got, want)
		}
	}
	if got := Inspect("https://github.com/o/r").String(); got != "no userinfo" {
		t.Errorf("Finding.String() = %q", got)
	}
}

func TestItoa(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 40: "40", 1234: "1234"} {
		if got := itoa(n); got != want {
			t.Errorf("itoa(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestShapesIsACopy: a caller that mutated the table would change what every
// other tool on the machine refuses.
func TestShapesIsACopy(t *testing.T) {
	s := Shapes()
	s[0].Prefix = "zzz_"
	if Shapes()[0].Prefix == "zzz_" {
		t.Error("Shapes returned the table itself")
	}
}
