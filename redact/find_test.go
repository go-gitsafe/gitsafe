package redact

import (
	"strings"
	"testing"
)

// maskSpans replaces each span with Mask.
func maskSpans(s string, spans []Span) string {
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		b.WriteString(s[last:sp.Start])
		b.WriteString(Mask)
		last = sp.End
	}
	b.WriteString(s[last:])
	return b.String()
}

func TestFindAgreesWithString(t *testing.T) {
	r := New("correct-horse-battery")
	for _, s := range []string{
		"no secret here",
		"token ghp_" + strings.Repeat("a", 36) + " and more",
		"push to https://x-access-token:ghs_" + strings.Repeat("B", 36) + "@github.com/o/r",
		"https://alice:hunter2hunter2@example.org/x and correct-horse-battery",
		"glpat-" + strings.Repeat("x", 20) + ".01.abcdef then AKIA" + strings.Repeat("Q", 16),
	} {
		spans := r.Find(s)
		if got, want := maskSpans(s, spans), r.String(s); got != want {
			t.Errorf("Find masks %q\n         String masks %q", got, want)
		}
	}
}

func TestFindKinds(t *testing.T) {
	tok := "ghp_" + strings.Repeat("a", 36)
	s := "a " + tok + " b https://u:pw12345678@h/ c secret-value-1"
	spans := New("secret-value-1").Find(s)
	want := []Kind{Token, URLCredential, Literal}
	if len(spans) != len(want) {
		t.Fatalf("%+v", spans)
	}
	for i, sp := range spans {
		if sp.Kind != want[i] {
			t.Errorf("span %d: %s, want %s", i, sp.Kind, want[i])
		}
	}
	if s[spans[0].Start:spans[0].End] != tok || s[spans[1].Start:spans[1].End] != "u:pw12345678" {
		t.Errorf("spans %+v", spans)
	}
}

func TestFindMergesOverlaps(t *testing.T) {
	tok := "ghp_" + strings.Repeat("a", 36)
	// The literal is inside the token: one span, the token's.
	spans := New(tok[4:20]).Find("x " + tok + " y")
	if len(spans) != 1 || spans[0].Start != 2 || spans[0].End != 2+len(tok) || spans[0].Kind != Token {
		t.Fatalf("%+v", spans)
	}
}

func FuzzFind(f *testing.F) {
	f.Add("token ghp_" + strings.Repeat("a", 36))
	f.Add("https://a:b@c https://x:y@z")
	f.Add("glpat-" + strings.Repeat("x", 20) + ".01.abcdef")
	f.Add("lit lit lit")
	r := New("lit-lit-lit")
	f.Fuzz(func(t *testing.T, s string) {
		spans := r.Find(s)
		end := 0
		for _, sp := range spans {
			if sp.Start < end || sp.End <= sp.Start || sp.End > len(s) {
				t.Fatalf("bad span %+v after %d in %q", sp, end, s)
			}
			end = sp.End
		}
		// Once masked, nothing shaped like a secret is left.
		masked := maskSpans(s, spans)
		for _, sp := range r.Find(masked) {
			if sp.Kind != Literal {
				t.Fatalf("%q masked to %q still has %+v", s, masked, sp)
			}
		}
	})
}
