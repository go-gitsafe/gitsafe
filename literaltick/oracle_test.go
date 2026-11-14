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

var span = regexp.MustCompile("`[^`'\"\\\\$]*`")

// runs reports whether shell executed a backtick of cmd, by the file the
// substitution creates. Each command gets its own directory and a timeout.
func runs(t *testing.T, shell, cmd string) bool {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, shell, "-c", cmd)
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
	var shells []string
	for _, s := range []string{"bash", "dash"} {
		if p, err := exec.LookPath(s); err == nil {
			shells = append(shells, p)
		} else {
			t.Logf("no %s here: its half of the oracle is skipped", s)
		}
	}
	if len(shells) == 0 {
		t.Skip("neither bash nor dash is installed")
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
