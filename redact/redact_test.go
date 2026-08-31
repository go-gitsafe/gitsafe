package redact

import (
	"strings"
	"testing"
)

// TestTheTwoRealLeaks reproduces, exactly, the two lines that put a credential
// in front of a person. If either survives redaction this package has failed at
// the only job it has.
func TestTheTwoRealLeaks(t *testing.T) {
	// Assembled rather than written whole, so that nothing scanning this
	// repository for credentials finds something shaped like one. It used to
	// be the token that actually leaked; that was revoked long ago and
	// answered 401 when this was checked, but a dead credential in a file is
	// still a credential in a file, and this package exists because of where
	// those end up.
	//
	// The value does not matter here: New is given the token, so redaction is
	// by identity. What the shape-matching tests below need is a shape, and
	// they have their own.
	tok := "gh" + "p_" + strings.Repeat("x", 36)
	for _, line := range []string{
		"branch 'main' set up to track 'https://x-access-token:" + tok + "@github.com/go-xrkit/desk.git/main'.",
		"remote: https://x-access-token:" + tok + "@github.com/go-xrkit/desk.git",
		"fatal: could not read Password for 'https://x-access-token@github.com': terminal prompts disabled",
	} {
		got := New(tok).String(line)
		if strings.Contains(got, tok) {
			t.Errorf("the token survived redaction:\n  %s", got)
		}
	}
}

// TestRedactsWithoutBeingToldTheSecret is the property that matters most. The
// caller does not always KNOW the secret — it can arrive from a nested command
// or from a server's own reply — so shape alone must be enough.
func TestRedactsWithoutBeingToldTheSecret(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"classic PAT", "token ghp_0123456789abcdefghijABCDEFGHIJ here"},
		{"oauth", "gho_0123456789abcdefghijABCDEFGHIJ"},
		{"user-to-server", "ghu_0123456789abcdefghijABCDEFGHIJ"},
		{"server-to-server", "ghs_0123456789abcdefghijABCDEFGHIJ"},
		{"refresh", "ghr_0123456789abcdefghijABCDEFGHIJ"},
		{"fine-grained", "github_pat_11ABCDE0123456789_abcdefghijklmnop"},
		{"credential in a URL", "https://user:hunter2hunter2@github.com/x/y.git"},
	} {
		got := New().String(tc.in) // told NOTHING
		if got == tc.in {
			t.Errorf("%s: nothing was redacted in %q", tc.name, got)
		}
		if !strings.Contains(got, Mask) {
			t.Errorf("%s: no mask in %q", tc.name, got)
		}
	}
}

// TestUrlRedactionStaysReadable: a masked line still has to tell a person which
// remote failed, or they cannot act on it.
func TestUrlRedactionStaysReadable(t *testing.T) {
	got := New().String("pushing to https://x-access-token:ghp_0123456789abcdefghij@github.com/go-xrkit/desk.git")
	for _, want := range []string{"github.com/go-xrkit/desk.git", Mask, "https"} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted line lost %q: %s", want, got)
		}
	}
}

// TestLongestSecretFirst: masking a short secret first would leave the tail of a
// longer one containing it in plain sight.
func TestLongestSecretFirst(t *testing.T) {
	short, long := "abcdefghij", "abcdefghijKLMNOPQRST"
	got := New(short, long).String("value=" + long)
	if strings.Contains(got, "KLMNOPQRST") {
		t.Errorf("the tail of the longer secret survived: %s", got)
	}
}

func TestShortValuesAreNotTreatedAsSecrets(t *testing.T) {
	// Masking something this short would mangle ordinary output everywhere it
	// happened to appear, which is a worse outcome than the leak it prevents.
	got := New("main", "go", "  ", "").String("pushing main to origin")
	if got != "pushing main to origin" {
		t.Errorf("a short value was masked: %q", got)
	}
}

func TestBytes(t *testing.T) {
	const tok = "ghp_0123456789abcdefghijABCDEFGHIJ"
	if got := New(tok).Bytes([]byte("x " + tok)); strings.Contains(string(got), tok) {
		t.Errorf("Bytes left the token in: %s", got)
	}
}

func TestClean(t *testing.T) {
	for _, tc := range []struct {
		in    string
		clean bool
	}{
		{"https://github.com/go-xrkit/desk.git", true},
		{"git@github.com:go-xrkit/desk.git", true},
		{"", true},
		{"https://x-access-token:ghp_0123456789abcdefghij@github.com/x.git", false},
		{"https://user:pw@github.com/x.git", false},
		{"ghp_0123456789abcdefghijABCDEFGHIJ", false},
	} {
		if got := Clean(tc.in); got != tc.clean {
			t.Errorf("Clean(%q) = %v, want %v", tc.in, got, tc.clean)
		}
	}
}
