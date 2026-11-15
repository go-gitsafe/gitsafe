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
// So the rule is: a comment LINE with a ) is unknown (refused) when the " pairs
// formed from its # to the END OF THE BODY enclose a | & newline ' \ or a
// backtick, or leave a " unpaired; every other comment hides its ). (The rows
// with a space, ( ) ; or a comma in the span are cut by bash but ran nothing,
// and are allowed.) Two further rules sit beside it: an odd number of " on the
// comment line with a ) later in the body (V2), and a backtick inside " owning
// the next ". Each row DECLARES the
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
		// FuzzBody, #133: the span holds a ; and a #, which comments out the rest
		// of the command line after the cut (bash 3.2.57 RAN each of the first
		// three; `a#b` and a lone ; ran nothing and are allowed: only a # after a
		// ; & ( or a blank starts a comment).
		{"a ;# in the span, a newline and the marker", "#)\";#\"\n" + T, true},
		{"a blank and # in the span", "#)\" #\"\n" + T, true},
		{"the ) earlier in a longer comment", "# note ) \";#\"\n" + T, true},
		{"a # inside a word of the span starts no comment", "#)\"a#b\"\n" + T, false},
		{"a ; alone in the span", "#)\";\"\n" + T, false},
		// FuzzBody, #133: an odd ' on the comment line pairs with the next ', and
		// the ) after it is bare (bash 3.2.57 ran the first two).
		{"an odd ' then a second ' and )\"|", "#'\n')\"|" + T, true},
		{"the same after a word and a blank", "x #'\nx')\"|" + T, true},
		{"an odd ' then a second ' and )\"& (FuzzBody 9533303894e0bbea)", "#'\n')\"&" + T, true},
		{"an odd ' and no second ' later", "# it's\n(x)\"|" + T, false},
		{"an odd ' then a second ' and )\" then a newline (FuzzBody b0adc02f8140116d)", "#'\n')\"\n" + T, true},
		{"an odd backtick then a second one and )\" then a newline", "#`\n`)\"\n" + T, true},
		{"an escaped \" then a \" and a pipe (FuzzBody 8c166a2c1744b998)", "#)\\\"\"|" + T, true},
		{"an escaped ' then a ' makes the count odd (FuzzBody 1507c57c2efc7d6f)", "#\\''\n')\"|" + T, true},
		{"an even run of backslashes does not escape the ' (FuzzBody d8e15c80da0cee1d)", "#\\\\'\n')\"|" + T, true},
		{"an odd backtick then a second one and )\"|", "#`\n`)\"|" + T, true},
		{"an odd ' and a second ' but no \" after the )", "#'\n')" + T, false},
		// V2: an ODD number of " on the comment line and a ) later in the body.
		// Measured with gitsafe-133-probe/span under bash 3.2.57 (ran = true for all
		// three): a second comment's " does not close the first.
		{"V2: two comment lines, quotes pairing across", "#\"\n#\"\n)\"|" + T, true},
		{"V2: a second comment line with a blank before its \"", "#\"\n# \"\n)\"|" + T, true},
		{"V2: a closed pair, then a fresh odd comment line", "# \"a\"\n#\"\n\")\"|" + T, true},
		{"V2: a lone \" then a line holding \")\"|", "#\"\n\")\"|" + T, true},
		{"V2: after a word line", "#\"\nx\")\"|" + T, true},
		{"V2: after a word and blanks", "x #\"\n\")\"|" + T, true},
		{"V2: three \" on the comment line", "# \"a\" \"\n\")\"|" + T, true},
		{"V2: the ) only after a further line", "#\"\nx\ny)\"|" + T, true},
		{"V2: a lone \" and no ) anywhere later", "#\"\nx\"|" + T, false},
		{"V2: an even count of \" and a ) later", "# \"a\"\n\")\"|" + T, false},
		// A backtick inside " owns the second ": the ) after the third is bare.
		{"dq-backtick: `\"`\"`\")` then the marker", "\"`\"`\")" + T, true},
		{"dq-backtick: the span closes, the ) is inside it", "\"`\")`\"" + T, false},
		{"dq-backtick: an escaped backtick does not open the span", "\"\\`\")" + T, true},
		{"dq-backtick: spans that never close", "\"`\"|" + T, false},
	} {
		cmd := wrap(tc.body)
		if got := check(cmd).Found(); got != tc.refuse {
			t.Errorf("%s: the rule refuses = %v, declared %v\n  %q", tc.name, got, tc.refuse, cmd)
		}
	}
}

// The rows of #133's second list were MEASURED to run their backtick under
// bash 3.2.57. The rule must refuse each, and a shell that runs it is the
// corroboration; a machine whose bash is not 3.2 says so rather than pass.
func TestV2AndDqBacktickAreRealCuts(t *testing.T) {
	shells := oracleShells(t)
	if len(shells) == 0 {
		t.Skip("no shell to ask")
	}
	for name, body := range map[string]string{
		"an odd ' after a comment":      "#'\n')\"|" + T,
		"V2 lone \" after a comment":    "#\"\n\")\"|" + T,
		"V2 after a word line":          "#\"\nx\")\"|" + T,
		"V2 after a word and blanks":    "x #\"\n\")\"|" + T,
		"a backtick inside \" owns it":  "\"`\"`\")" + T,
		"the same after a plain prefix": "x\"`\"`\")" + T,
	} {
		cmd := "echo --body \"$(cat <<'EOF'\n" + body + "\nEOF\n)\"\n"
		if !check(cmd).Found() {
			t.Errorf("%s: the rule allows %q", name, cmd)
		}
		ran := false
		for _, sh := range shells {
			ran = ran || runs(t, sh, cmd)
		}
		t.Logf("%s: an installed shell ran the backtick = %v", name, ran)
	}
}

// The FuzzBody corpus entries for the four seeds of #133 decode to the bodies
// the issue names: a wrong token index would commit a different body.
func TestCommentQuoteCorpusDecodes(t *testing.T) {
	for name, want := range map[string]string{
		"issue133-seed1":   "#)\"|" + T,
		"issue133-seed2":   "x\n#)\"|" + T,
		"issue133-seed3":   "t #)\"|" + T,
		"issue133-seed4":   "#;)\"|" + T,
		"seed-589e8577":    "#)\"|" + T,
		"f6a028469111f9dd": "#)\";#\"\n" + T,
		"1d3d1b483c5655cf": "#'\n')\"|" + T,
		"9533303894e0bbea": "#'\n')\"&" + T,
		"b0adc02f8140116d": "#'\n')\"\n" + T,
		"bt-newline":       "#`\n`)\"\n" + T,
		"8c166a2c1744b998": "#)\\\"\"|" + T,
		"1507c57c2efc7d6f": "#\\''\n')\"|" + T,
		"d8e15c80da0cee1d": "#\\\\'\n')\"|" + T,
		"bt-pipe":          "#`\n`)\"|" + T,
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
