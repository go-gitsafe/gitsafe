package plain

import "testing"

// heredocOpener accepts a quoted delimiter that is a plain word and ends there,
// and nothing else: every shape below would make the shell read the body
// differently, so each must be declined.
func TestHeredocOpenerDeclinesWhatItCannotPlace(t *testing.T) {
	for _, tc := range []struct {
		in       string
		word     string
		ok       bool
		whatItIs string
	}{
		{`'E'`, "E", true, "quoted word"},
		{`"E"`, "E", true, "double-quoted word"},
		{`\E`, "E", true, "backslashed word"},
		{`'E';`, "E", true, "followed by ;"},
		{`'E')`, "E", true, "followed by )"},
		{`'a b'`, "", false, "a space inside the quotes"},
		{`'a.b'`, "", false, "a dot inside the quotes"},
		{`'abc`, "", false, "an unterminated quote"},
		{`''`, "", false, "an empty delimiter"},
		{`\;`, "", false, "a backslash before a non-word byte"},
		{`\`, "", false, "a lone backslash"},
		{`'E'x`, "", false, "a word that goes on past the quote"},
		{`\Ex$`, "", false, "a backslashed word that goes on past the end"},
		{`E`, "", false, "an unquoted word"},
		{``, "", false, "nothing"},
	} {
		word, _, ok := heredocOpener(tc.in, 0)
		if ok != tc.ok || word != tc.word {
			t.Errorf("%s: heredocOpener(%q) = %q, %v; want %q, %v", tc.whatItIs, tc.in, word, ok, tc.word, tc.ok)
		}
	}
}

// A backslash that ends the command inside double quotes is a construct the
// grammar cannot place, and says so by name.
func TestATrailingBackslashInDoubleQuotesIsNamed(t *testing.T) {
	got := check("echo \"`x`\\", false)
	if got.V != Uncertain || got.Reason != "trailing backslash" {
		t.Errorf("got %v %q", got.V, got.Reason)
	}
}
