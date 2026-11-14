package shellcmd

import (
	"strings"
	"testing"
)

// A quoted heredoc is literal, so text that QUOTES a forbidden form is not the
// forbidden form. Documentation, a commit message and a note all need to say
// what not to do — including in this repository's own history.
func TestAQuotedHeredocBodyIsRemoved(t *testing.T) {
	for _, tag := range []string{"<<'EOF'", `<<"EOF"`, `<<\EOF`, "<<-'EOF'"} {
		cmd := "cat " + tag + "\nthe forbidden line\nEOF\nafter"
		got := StripQuotedHeredocs(cmd)
		if strings.Contains(got, "the forbidden line") {
			t.Errorf("%s: the body survived: %q", tag, got)
		}
		if !strings.Contains(got, "after") {
			t.Errorf("%s: what followed the heredoc was lost: %q", tag, got)
		}
		if !strings.Contains(got, "cat ") {
			t.Errorf("%s: the opening line was lost, and it may carry a substitution of its own: %q", tag, got)
		}
	}
}

// An UNQUOTED heredoc does expand, so its body is what actually runs and must be
// left in place to be scanned.
func TestAnUnquotedHeredocBodyIsKept(t *testing.T) {
	got := StripQuotedHeredocs("cat <<EOF\nthe expanded line\nEOF")
	if !strings.Contains(got, "the expanded line") {
		t.Errorf("an unquoted body was removed: %q", got)
	}
}

func TestTextWithNoHeredocIsUntouched(t *testing.T) {
	const cmd = "git status --porcelain && echo done"
	if got := StripQuotedHeredocs(cmd); got != cmd {
		t.Errorf("StripQuotedHeredocs changed a plain command: %q", got)
	}
}

// TestInsideQuotes asks about the offset of a marker, so each case reads as
// the sentence it is: the marker is `WORD`, and the question is whether the
// shell would interpret something written there.
func TestInsideQuotes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cmd   string
		quote bool
	}{
		{"bare", "echo WORD", false},
		{"in single quotes", "echo 'a WORD b'", true},
		{"in double quotes", `echo "a WORD b"`, true},
		// ⛔ The measured false positive: a message that TALKS about a loop.
		{"an English sentence in double quotes", `agentsync tell repo "green WORD tests run"`, true},
		{"after a closed single-quoted string", "echo 'done' WORD", false},
		{"after a closed double-quoted string", `echo "done" WORD`, false},
		{"between two quoted strings", `echo "a" WORD "b"`, false},
		// ⛔ Double quotes do NOT stop a substitution: the shell runs this.
		{"in a substitution inside double quotes", `echo "$(find . -name WORD)"`, false},
		{"after the substitution closes, still quoted", `echo "$(ls) WORD"`, true},
		{"in a backtick substitution inside double quotes", "echo \"`grep WORD f`\"", false},
		{"in a substitution in single quotes is literal", `echo '$(find WORD)'`, true},
		{"nested substitutions", `echo "$(echo "$(ls WORD)")"`, false},
		{"an escaped quote does not end the quoting", `echo "a \" WORD b"`, true},
		// ⛔ Unsure means refuse: an unbalanced quote is a broken command.
		{"an unbalanced single quote", "echo 'a WORD b", false},
		{"an unbalanced double quote", `echo "a WORD b`, false},
		{"an unclosed substitution", `echo "$(ls WORD"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			off := strings.Index(tc.cmd, "WORD")
			if off < 0 {
				t.Fatalf("the case has no marker in it: %q", tc.cmd)
			}
			if got := InsideQuotes(tc.cmd, off); got != tc.quote {
				t.Errorf("InsideQuotes(%q, %d) = %v, want %v", tc.cmd, off, got, tc.quote)
			}
		})
	}
}

// An offset nobody can point at is not inside anything.
func TestInsideQuotesOutOfRange(t *testing.T) {
	for _, off := range []int{-1, 0, 5, 1000} {
		if InsideQuotes("", off) {
			t.Errorf("InsideQuotes(%q, %d) = true", "", off)
		}
	}
	if InsideQuotes("'abc'", 5) {
		t.Error("an offset one past the end reads as quoted")
	}
}

// ⛔ Stages has always known both quote characters; this says so out loud,
// because the loop rule once disagreed with it about the same sentence.
func TestStagesAndInsideQuotesAgreeAboutDoubleQuotes(t *testing.T) {
	const cmd = `agentsync tell repo "redirect rather than | head"`
	if n := len(Stages(cmd)); n != 1 {
		t.Errorf("Stages found %d stages: a pipe inside double quotes is an argument", n)
	}
	if !InsideQuotes(cmd, strings.Index(cmd, "|")) {
		t.Error("InsideQuotes disagrees with Stages about the same pipe")
	}
}

// ⛔ #112: an opener INSIDE quotes opens nothing, so the next line is a command
// and must not be stripped as a heredoc body.
func TestAFakeHeredocOpenerInsideQuotesStripsNothing(t *testing.T) {
	for _, cmd := range []string{
		"echo \"<<'EOF'\"\necho RAN\nEOF\necho after",
		"echo '<<\"EOF\"'\necho RAN\nEOF\necho after",
		"echo hi # <<'EOF'\necho RAN\nEOF",
	} {
		if got := StripQuotedHeredocs(cmd); got != cmd {
			t.Errorf("stripped text that opens no heredoc:\n  in:  %q\n  out: %q", cmd, got)
		}
	}
}

// ⛔ #112: a heredoc tag is a shell WORD, not an identifier.
func TestAHeredocTagMayHoldAHyphenOrADot(t *testing.T) {
	for _, tc := range []struct{ open, tag string }{
		{"<<'END-MSG'", "END-MSG"},
		{`<<"a.b"`, "a.b"},
		{`<<\END-MSG`, "END-MSG"},
		{"<<-'x-y'", "x-y"},
		{"<<'A'\"B\"", "AB"},
	} {
		got := StripQuotedHeredocs("cat " + tc.open + "\nforbidden\n" + tc.tag + "\nafter")
		if strings.Contains(got, "forbidden") || !strings.Contains(got, "after") {
			t.Errorf("%s: %q", tc.open, got)
		}
	}
}

// Shifts and here-strings are not heredocs, and swallowing the rest of the
// command for one would hide what follows.
func TestShiftsAndHereStringsAreNotHeredocs(t *testing.T) {
	for _, cmd := range []string{
		"echo $((1<<n))\necho after",
		"echo $((x << 2))\nn\necho after",
		"x=$((1<<2))\n2\necho after",
		"cat <<<'text'\necho after",
		"echo 1<<2\n2",
	} {
		if got := StripQuotedHeredocs(cmd); got != cmd {
			t.Errorf("changed %q to %q", cmd, got)
		}
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		name, cmd, want string // want: one class byte per byte of cmd
	}{
		{"code, single, double", `a 'b' "c"`, "ccssscddd"},
		{"escaped", `a\'b`, "ccec"},
		{"ansi-c with an escaped quote", `$'a\'b'`, "sssssss"},
		{"comment", "a #b\nc", "cckkcc"},
		{"substitution inside double quotes", `"$(a 'b')"`, "dccccssscd"},
		{"unterminated single quote is code", `'a`, "cc"},
		{"quoted heredoc body", "<<'E'\nx\nE\ny", "cccccchhhhc"},
		{"expanded heredoc body", "<<E\n'x\nE\ny", "ccccuuuccc"},
		{"escape inside double quotes", `"\""`, "dded"},
		{"an unclosed frame is all code", `"a 'b'`, "cccccc"},
		// ⛔ THE ) IN THE PROSE. A quoted heredoc inside $( … ) keeps its body
		// through a ) in it: zsh, bash and dash all take the quoted body as a
		// unit, and reading that ) as the end of the substitution turned every
		// `--body "$(cat <<'EOF' … )"` with a parenthesis in its prose into code
		// from that byte on — 5 050 refusals of 554 998 commands measured.
		{"a ) inside a quoted heredoc body", "\"$(cat <<'E'\n(x)\nE\n)\"", "dcccccccccccchhhhhhcd"},
		// The doubt is kept for an UNQUOTED body, where the shell expands what
		// it reads and this lexer's model of it is weaker.
		{"a ) inside an expanded heredoc body", "\"$(cat <<E\n(x)\nE\n)\"", "dcccccccccccccccccc"},
	} {
		if got := string(Classify(tc.cmd)); got != tc.want {
			t.Errorf("%s: Classify(%q) =\n  %q, want\n  %q", tc.name, tc.cmd, got, tc.want)
		}
	}
}

func FuzzClassify(f *testing.F) {
	for _, s := range []string{"echo 'a'", "\"$(a 'b')\"", "$'a\\'b'", "cat <<'E'\nx\nE", "cat <<E\n`x`\nE", "# c", "<<", "$((1<<2))", "\\"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		if cls := Classify(cmd); len(cls) != len(cmd) {
			t.Fatalf("%d classes for %d bytes of %q", len(cls), len(cmd), cmd)
		}
		for i, c := range Classify(cmd) {
			if !strings.ContainsRune("cdskhue", rune(c)) {
				t.Fatalf("byte %d of %q has no class (%q)", i, cmd, c)
			}
		}
		if got := StripQuotedHeredocs(cmd); len(got) > len(cmd) {
			t.Fatalf("stripping grew %q into %q", cmd, got)
		}
		if StripQuotedHeredocs(cmd) != StripQuotedHeredocs(cmd) {
			t.Fatalf("not deterministic: %q", cmd)
		}
	})
}
