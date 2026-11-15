package literaltick

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tables in literaltick_test.go call check, which records what it was asked
// so the oracle below can judge every one of them against real shells.
var (
	recMu    sync.Mutex
	recorded []string
)

func check(cmd string) Finding {
	recMu.Lock()
	recorded = append(recorded, cmd)
	recMu.Unlock()
	return Check(cmd)
}

// T is a substitution whose only effect is to create a file named ran.
const T = "`:>ran`"

// reviewCases are the inputs of the review of #120, each a place where a
// classifier that is wrong disagrees with the shell.
func reviewCases() []string {
	B := "`"
	return []string{
		`echo a\ #` + T,
		"echo a\\\n#" + T,
		`echo $(true)#` + T,
		`echo $((1))#` + T,
		`echo $$'a\'` + T + `''`,
		`echo "${x:-"'"}" ` + T + ` "${x:-"'"}"`,
		`echo "` + B + `printf %s "'"` + B + `" ` + T + ` "` + B + `printf %s "'"` + B + `"`,
		`echo "$(case x in x) echo "'` + T + `'";; esac)"`,
		`echo "$(case x in (x) echo "'` + T + `'";; esac)"`,
		"(( 1 <<'E' ))\necho " + T + "\nE",
		"echo ${x/<<'E'/}\necho " + T + "\nE",
		"cat <<'E'\n E\n'\nE\necho " + T + "; : '\n'",
		"echo \"<<'EOF'\"\necho " + T,
		"GITSAFE_ALLOW_BACKTICK=1 true; echo " + T,
		"GITSAFE_ALLOW_BACKTICK=1 true >&2 echo " + T,
		"echo x#" + T,
		"# c " + T,
		"echo \\\n#" + T,
		"cat <<'A' <<B\n" + T + "\nA\n" + T + "\nB",
		"cat <<A <<'B'\n" + T + "\nA\nx\nB",
		"cat <<-'E'\n\t" + T + "\n\tE",
		"cat <<-'E'\n\t" + T + "\n  E\nE",
		"cat <<'END-MSG'\n" + T + "\nEND-MSG",
		`echo $'a\'` + T + `'`,
		`echo "$(grep '` + T + `' /dev/null)"`,
		"echo '" + T,
		"echo \"" + T,
		"echo $(echo ')'; echo " + T + ")",
		"echo $(( 2 )) ; echo '" + T + "'",
		"x=$(cat <<'E'\n" + T + "\nE\n)",
		"echo ${x:-'" + T + "'}",
		"echo \"${x:-'" + T + "'}\"",
		"echo $\"" + T + "\"",
		"echo a;#" + T,
		"echo a&#" + T,
		"echo `echo '`'` " + T,
		`echo "$(echo case)'` + T + `'"`,
		`echo "$(: case)'` + T + `'"`,
		`echo "$(echo 'case')'` + T + `'"`,
		`echo "$(echo x.case)'` + T + `'"`,
		`echo "$(case x in x) :;; esac)'` + T + `'"`,
		`echo "$(echo "$(case x in x) :;; esac)")'` + T + `'"`,
		`echo "$(case x in (x) :;; esac)'` + T + `'"`,
		// #121: found by zsh, where `(#` opens a glob flag and `a(` a glob
		// qualifier, and the backticks are substituted before the bad pattern is
		// reported; bash reads the same # as a comment.
		`\$(#` + T,
		`echo a(#` + T,
		`echo (#` + T,
		`echo \$(#` + T + `)`,
		`: (|#` + T,
		`echo a|#` + T,
	}
}

func corpusCases(t *testing.T) []string {
	files, _ := filepath.Glob(filepath.Join("testdata", "fuzz", "FuzzCheck", "*"))
	if len(files) == 0 {
		t.Fatal("the FuzzCheck corpus is empty or unreadable: the oracle would judge less than it claims")
	}
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		_, body, _ := strings.Cut(string(b), "\n")
		body = strings.TrimSpace(body)
		body = strings.TrimSuffix(strings.TrimPrefix(body, "string("), ")")
		s, err := strconv.Unquote(body)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, s)
	}
	return out
}

// oracleShells are the shells the oracle asks. The rule refuses what ANY of them
// would execute, so the expected result is never derived from which are
// installed. One that is absent is LOGGED BY NAME (visible with -v), never
// counted as a pass; CI installs zsh so that it really runs there.
func oracleShells(t testing.TB) []string {
	var shells []string
	for _, s := range []string{"bash", "dash", "zsh"} {
		if p, err := exec.LookPath(s); err == nil {
			shells = append(shells, p)
		} else {
			t.Logf("SKIPPED %s: not installed here, its half of the oracle did not run", s)
		}
	}
	return shells
}

var span = regexp.MustCompile("`[^`'\"\\\\$]*`")

// runs reports whether shell executed a backtick of cmd, by the file the
// substitution creates. Each command gets its own directory and a timeout.
func runs(t *testing.T, shell, cmd string) bool {
	t.Helper()
	// ⛔ dash has no $'…': it reads the $ as a character and the quote as an
	// ordinary one, so it and bash cannot both be right about `$'a\'T'`. The
	// rule follows bash (and zsh), the shells this guard protects; asking dash
	// about ANSI-C quoting would contradict the issue's own false refusal.
	if strings.HasSuffix(shell, "dash") && strings.Contains(cmd, "$'") {
		return false
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"-c", cmd}
	if strings.HasSuffix(shell, "zsh") {
		// ⛔ zsh runs as `zsh -f -c`: -f is NO_RCS, so no ~/.zshenv or ~/.zshrc
		// can change the result. It is a non-interactive shell with zsh's DEFAULT
		// options: `interactivecomments` is off and does not apply, so `#` does
		// not start a comment mid-line in any zsh run here, and that is the very
		// property the rule's comment handling must not depend on.
		args = []string{"-f", "-c", cmd}
	}
	c := exec.CommandContext(ctx, shell, args...)
	c.Dir = dir
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir}
	_ = c.Run() // a failing command is fine: only the marker matters
	_, err := os.Stat(filepath.Join(dir, "ran"))
	return err == nil
}

// ⛔ THE GATE, NOT A LIST. The review of #120 found four regressions by hand,
// each a place where this package's reading of the line differs from the
// shell's. A list of those four stays green when the fifth arrives; asking
// bash and dash does not. For every command with a backtick — the tables, the
// fuzz corpus, the review's cases — if either shell RAN the backtick, the rule
// must refuse.
//
// The command's own backtick spans are rewritten to T first, so "ran" is
// observable; a span holding a quote or a $ is left alone and proves nothing.
func TestTheRuleRefusesWhateverARealShellRuns(t *testing.T) {
	shells := oracleShells(t)
	if len(shells) == 0 {
		t.Skip("none of bash, dash, zsh is installed")
	}
	seen := map[string]bool{}
	cases := append(reviewCases(), corpusCases(t)...)
	recMu.Lock()
	cases = append(cases, recorded...)
	recMu.Unlock()
	var ran int
	for _, c := range cases {
		if !strings.Contains(c, "`") || seen[c] {
			continue
		}
		seen[c] = true
		c = span.ReplaceAllString(c, T)
		// The escape is an act that allows ITS statement; judge the parser with
		// the escape renamed, and the escape itself in the tables.
		judged := strings.ReplaceAll(c, Escape+"=1", "FOO=1")
		for _, sh := range shells {
			if !runs(t, sh, judged) {
				continue
			}
			ran++
			if !Check(judged).Found() {
				t.Errorf("%s RAN a backtick and the rule allowed it:\n  %q", filepath.Base(sh), judged)
			}
		}
	}
	if ran == 0 {
		t.Fatal("no shell ran any backtick of any case: the oracle cannot see what it is for")
	}
}

// oracleTokens is the SAFE grammar of FuzzOracle: no command but echo, : and
// true, no file but ran. One input byte picks one token, so the fuzzer's
// mutations are edits to a token sequence, and every program it builds can do
// nothing worse than create ./ran in its own directory.
var oracleTokens = []string{
	"echo ", ": ", "true ", "case x in x) ", ";; esac", "$(", ")", "$((", "))",
	"${x:-", "}", "'", "\"", "$'", "\\", "#", ";", "|", "&&", "\n",
	"<<'E'\n", "<<E\n", "<<-E\n", "E\n", "\tE\n", " ", "x", "(", "(( ",
	T, T, T,
}

func oracleProgram(data []byte) string {
	var b strings.Builder
	for _, c := range data {
		b.WriteString(oracleTokens[int(c)%len(oracleTokens)])
	}
	return b.String()
}

func oracleSeed(toks ...string) []byte {
	var out []byte
	for _, tok := range toks {
		found := false
		for i, o := range oracleTokens {
			if o == tok {
				out = append(out, byte(i))
				found = true
				break
			}
		}
		if !found {
			panic("not a token: " + tok)
		}
	}
	return out
}

// ⛔ THE DIFFERENTIAL FUZZ. TestTheRuleRefusesWhateverARealShellRuns judges the
// cases somebody thought of; this one judges the cases nobody did. Each program
// is built from the safe grammar above and run under bash, dash and zsh exactly as
// the oracle does (own directory, 5 s, stdin closed). It fails when either
// shell created the marker and the rule allowed the command.
func FuzzOracle(f *testing.F) {
	shells := oracleShells(f)
	for _, seed := range [][]byte{
		oracleSeed("echo ", "\"", "$(", "echo ", "x", ")", "'", T, "'", "\""),
		oracleSeed("echo ", "\"", "$(", ": ", "x", ")", "'", T, "'", "\""),
		oracleSeed("echo ", "\"", "$(", "case x in x) ", ": ", ";; esac", ")", "'", T, "'", "\""),
		oracleSeed("echo ", "\\", " ", "#", T),
		oracleSeed("echo ", "$(", "true ", ")", "#", T),
		oracleSeed("echo ", "$'", "x", "\\", "'", T, "'", "'"),
		oracleSeed("echo ", "<<'E'\n", " ", "E\n", "'", "\n", "E\n", "echo ", T, ";", ": ", "'", "\n", "'"),
		oracleSeed("echo ", "\"", "${x:-", "\"", "'", "\"", "}", "\"", " ", T),
		oracleSeed("(( ", "<<'E'\n", "))", "\n", "echo ", T),
		oracleSeed("echo ", "\"", "<<'E'\n", "\"", "\n", "echo ", T),
		oracleSeed("true ", ";", "echo ", T),
		oracleSeed("<<-E\n", "\tE\n", "echo ", T),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(shells) == 0 || len(data) > 40 {
			t.Skip()
		}
		cmd := oracleProgram(data)
		if !strings.Contains(cmd, "`") || Check(cmd).Found() {
			return // nothing to judge, or refused: the safe direction
		}
		for _, sh := range shells {
			if runs(t, sh, cmd) {
				t.Fatalf("%s RAN a backtick and the rule allowed it:\n  %q", filepath.Base(sh), cmd)
			}
		}
	})
}
