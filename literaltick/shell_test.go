package literaltick

import (
	"path/filepath"
	"strings"
	"testing"
)

// CheckFor chooses the grammar from $SHELL (#156), and a backtick outside it is
// refused BY NAME. Rows on both sides of every boundary.
func TestCheckForChoosesTheGrammarFromTheShell(t *testing.T) {
	heredoc := "git commit -m \"$(cat <<'EOF'\nfix: use `x` here\nEOF\n)\""
	for _, tc := range []struct {
		name, shell, cmd string
		refuse           bool
		construct        string // substring of Finding.Construct when refused for the grammar
	}{
		{"no backtick", "/bin/zsh", "echo hi", false, ""},
		{"single quotes, zsh", "/bin/zsh", "echo '" + T + "'", false, ""},
		{"bare, zsh", "/bin/zsh", T, true, ""},
		{"double quotes, zsh", "/bin/zsh", "echo \"" + T + "\"", true, ""},
		{"a quoted heredoc is prose under zsh", "/bin/zsh", heredoc, false, ""},
		{"the same under bash is outside strict S", "/bin/bash", heredoc, true, "<<"},
		{"a login shell name", "-zsh", T, true, ""},
		{"zsh by bare name", "zsh", "echo '" + T + "'", false, ""},
		{"bash, in grammar", "/bin/bash", "echo '" + T + "'", false, ""},
		{"an unknown shell", "/bin/fish", "echo '" + T + "'", true, "fish"},
		{"an unset shell", "", "echo '" + T + "'", true, "unset"},
		{"a comment holding a backtick", "/bin/zsh", "echo a # " + T, false, ""},
		{"a # inside a word is not a comment", "/bin/zsh", "echo a#b " + T, true, "#"},
		{"ANSI-C quoting with an escaped quote", "/bin/zsh", "echo $'a\\'" + T + "'", false, ""},
		{"a backtick after the close of ANSI-C quoting", "/bin/zsh", "echo $'a' " + T, true, ""},
		{"a parenthesis", "/bin/zsh", "(echo '" + T + "')", true, "bare ("},
		{"an unquoted heredoc", "/bin/zsh", "cat <<EOF\n" + T + "\nEOF\n", true, "unquoted"},
		{"no backtick, unknown shell", "/bin/fish", "echo (x) # y", false, ""},
	} {
		f := CheckFor(tc.shell, tc.cmd)
		if f.Found() != tc.refuse {
			t.Errorf("%s: CheckFor(%q) refuses = %v, want %v\n  %q", tc.name, tc.shell, f.Found(), tc.refuse, tc.cmd)
			continue
		}
		if tc.construct != "" && !strings.Contains(f.Construct, tc.construct) {
			t.Errorf("%s: the construct %q does not name %q", tc.name, f.Construct, tc.construct)
		}
		if f.Found() && f.Why() == "" {
			t.Errorf("%s: a refusal with no reason", tc.name)
		}
		if f.Found() && f.Construct != "" && !strings.Contains(f.Why(), f.Construct) {
			t.Errorf("%s: the refusal does not name the construct %q: %s", tc.name, f.Construct, f.Why())
		}
	}
}

// The escape allows its own statement, as in Check; on a command the grammar
// cannot read it must cover EVERY backtick.
func TestCheckForHonoursTheEscapePrefix(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		refuse    bool
	}{
		{"in grammar, prefixed", Escape + "=1 echo " + T, false},
		{"in grammar, prefix =0", Escape + "=0 echo " + T, true},
		{"in grammar, the next statement is not covered", Escape + "=1 true; echo " + T, true},
		{"outside the grammar, prefixed", Escape + "=1 echo " + T + " # c", false},
		{"outside the grammar, a second backtick uncovered", Escape + "=1 echo " + T + " # c\necho " + T, true},
	} {
		if got := CheckFor("/bin/zsh", tc.cmd).Found(); got != tc.refuse {
			t.Errorf("%s: refuses = %v, want %v\n  %q", tc.name, got, tc.refuse, tc.cmd)
		}
	}
}

// ⛔ THE HISTORICAL FINDINGS, UNDER THE DECISION THAT NOW STANDS. Every input of the
// committed corpora and review tables that a real shell RUNS must be refused by the
// grammar chosen for that shell: the production zsh for S+, and bash and dash — bash
// 3.2 is the judge of the strict path — for S. Inputs that only bash 3.2 runs
// (#153, #154 among them) are exactly why bash gets S and not S+.
func TestCheckForRefusesWhateverTheShellOfItsGrammarRuns(t *testing.T) {
	shells := oracleShells(t)
	if len(shells) == 0 {
		t.Skip("none of bash, dash, zsh is installed")
	}
	seen := map[string]bool{}
	ran := 0
	for _, c := range append(reviewCases(), corpusCases(t)...) {
		if !strings.Contains(c, "`") || seen[c] {
			continue
		}
		seen[c] = true
		c = strings.ReplaceAll(span.ReplaceAllString(c, T), Escape+"=1", "FOO=1")
		for _, sh := range shells {
			if !runs(t, sh, c) {
				continue
			}
			ran++
			grammar := "/bin/bash"
			if isZsh(sh) {
				grammar = "/bin/zsh"
			}
			if !CheckFor(grammar, c).Found() {
				t.Errorf("%s RAN a backtick and CheckFor(%s) allowed it:\n  %q", filepath.Base(sh), grammar, c)
			}
		}
	}
	if ran == 0 {
		t.Fatal("no shell ran any backtick of any case: the oracle cannot see what it is for")
	}
}
