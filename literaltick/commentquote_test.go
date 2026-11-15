package literaltick

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ⛔ #133, SETTLED BY ASKING THE SHELLS with a probe that does not depend on a
// trailing |: the command is `echo "[$(cat <<'EOF'` BODY `EOF )]"` and what is
// observed is whether the substitution spans the whole body (the output is
// "[BODY]") and, separately, whether a backtick after it runs. Measured
// 2026-11-15, bash 3.2.57 / dash / zsh -f -c (dash and zsh span every row):
//
//	body                  bash spans   bash ran T
//	# ) note              yes          no     the comment hides the )
//	x\n# )  /  x # )      yes          no     same, at line start and after blanks
//	x;#)  /  x#)          NO           yes    no comment after ; or mid-word
//	# "a b" )             yes          no     a " span BEFORE the ) changes nothing
//	# ) "x"  # ) "a$b"    yes          no     a plain word in " " after it too
//	# it's  # " # `       NO           no     an odd quote breaks the parse, runs nothing
//	# ) '  /  # ) `       yes          no     after the ) an odd ' or ` is harmless
//	# ) "a b"  # ) "a,b"  NO           no     a space or , ( ) ; ' ` tab in " " after it
//	# ) "a|b"  "a&b"      NO           YES    a | or & does; so do #)"|T, #)"|"T,
//	  "a\nb"  #;)"|T                          and an unclosed " after the )
//
// So the rule is exactly: a comment with a ) and, after it, a " span that is
// not one plain word ([A-Za-z0-9_.$-]) or that never closes on the line, is
// unknown (refused); every other comment hides its ). Each row DECLARES the
// verdict; bash, the shell this was measured with, corroborates a refusal only
// by running the backtick (the rows where it does not run are refused too, as
// the rule cannot tell them apart from the ones that do).
func TestACommentWithAQuoteAndAParen(t *testing.T) {
	wrap := func(body string) string { return "echo --body \"$(cat <<'EOF'\n" + body + "\nEOF\n)\"\n" }
	for _, tc := range []struct {
		name, body string
		refuse     bool
	}{
		{"#133 seed 1: #) then \" and a pipe", "#)\"|" + T, true},
		{"#133 seed 2: after a line", "x\n#)\"|" + T, true},
		{"#133 seed 3: after a word and a space", "t #)\"|" + T, true},
		{"#133 seed 4: #; then )", "#;)\"|" + T, true},
		{"a ) then a lone \" after words", "# x ) \"|" + T, true},
		{"seed from the longer run: an empty-ish span", "#)\"|\"" + T, true},
		{"a | inside the span", "# ) \"a|b\"|" + T, true},
		{"a & inside the span", "# ) \"a&b\"|" + T, true},
		{"a newline inside the span", "# ) \"a\nb\"|" + T, true},
		{"the comment hides the )", "# note ) " + T, false},
		{"a plain word in the span", "# ) \"x\"|" + T, false},
		{"a quote BEFORE the )", "# \"a b\" )|" + T, false},
		{"a | span BEFORE the ): unsure, refused though bash ran nothing", "# \"|\" )|" + T, true},
		{"#133 third quote: a ) inside the first span", "#\")\")\"|" + T, true},
		{"#133 a ' inside a span then a lone '", "#)\"'\"'|" + T, true},
		{"a $ inside a plain span", "# ) \"a$b\"|" + T, false},
		{"an even count of \"", "# \"a\" )|" + T, false},
		{"an odd ' after the )", "# ) '|" + T, false},
		{"an odd ` after the )", "# ) `|" + T, false},
		{"an odd \" and no )", "# \"|" + T, false},
		{"a ) in a later line only", "x\n# \"a\"\n# ) note " + T, false},
	} {
		cmd := wrap(tc.body)
		if got := check(cmd).Found(); got != tc.refuse {
			t.Errorf("%s: the rule refuses = %v, declared %v\n  %q", tc.name, got, tc.refuse, cmd)
		}
	}
}

// The FuzzBody corpus entries for the four seeds of #133 decode to the bodies
// the issue names: a wrong token index would commit a different body.
func TestCommentQuoteCorpusDecodes(t *testing.T) {
	for name, want := range map[string]string{
		"issue133-seed1": "#)\"|" + T,
		"issue133-seed2": "x\n#)\"|" + T,
		"issue133-seed3": "t #)\"|" + T,
		"issue133-seed4": "#;)\"|" + T,
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "fuzz", "FuzzBody", name))
		if err != nil {
			t.Fatal(err)
		}
		_, body, _ := strings.Cut(string(b), "\n")
		body = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(body), "[]byte("), ")")
		s, err := strconv.Unquote(body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := bodyProgram([]byte(s)); got != want {
			t.Errorf("%s decodes to %q, want %q", name, got, want)
		}
	}
}
