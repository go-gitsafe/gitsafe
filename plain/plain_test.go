package plain

import (
	"context"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// T is a substitution whose only effect is to create a file named ran.
const T = "`:>ran`"

// runs reports whether the shell executed a backtick of cmd, by the file it
// creates. kind is "zsh" (the production wrapper) or "bash".
func runs(t testing.TB, kind, cmd string) bool {
	t.Helper()
	var argv []string
	switch kind {
	case "zsh":
		argv = []string{zshPath(t), "-c", ProductionScript(cmd)}
	case "bash":
		p, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("bash is not installed here")
		}
		argv = []string{p, "-c", cmd}
	default:
		t.Fatalf("unknown shell %q", kind)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir}
	_ = c.Run() // a failing command is fine: only the marker matters
	_, err := os.Stat(filepath.Join(dir, "ran"))
	return err == nil
}

// Each row is a verdict stated by hand, on both sides of every construct that
// leaves the grammar: the in-grammar neighbour is allowed or refused by value, the
// out-of-grammar one is uncertain and NAMED.
func TestVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		strict    Verdict // verdict of Check (S)
		plus      Verdict // verdict of CheckHeredoc (S+)
		reason    string  // substring of the S+ reason, when uncertain
	}{
		{"no backtick", "echo hi && ls | wc", NoTick, NoTick, ""},
		{"bare", T, Refuse, Refuse, ""},
		{"single quotes", "echo '" + T + "'", Allow, Allow, ""},
		{"double quotes", "echo \"" + T + "\"", Refuse, Refuse, ""},
		{"escaped", "echo \\" + T, Refuse, Refuse, ""}, // the second backtick of T is bare
		{"escaped pair", "echo \\`x\\`", Allow, Allow, ""},
		{"in $( )", "echo $(echo " + T + ")", Refuse, Refuse, ""},
		{"single inside $( )", "echo \"$(echo '" + T + "')\"", Allow, Allow, ""},
		{"nested $( )", "echo $(echo $(echo '" + T + "'))", Allow, Allow, ""},
		{"quoted heredoc", "cat <<'EOF'\n" + T + "\nEOF\n", Uncertain, Allow, "<<"},
		{"quoted heredoc in $( )", "x=\"$(cat <<'EOF'\n" + T + "\nEOF\n)\"", Uncertain, Allow, "<<"},
		{"escaped-word heredoc", "cat <<\\EOF\n" + T + "\nEOF\n", Uncertain, Allow, "<<"},
		{"dash heredoc", "cat <<-'EOF'\n\t" + T + "\n\tEOF\n", Uncertain, Allow, "<<"},
		{"unquoted heredoc", "cat <<EOF\n" + T + "\nEOF\n", Uncertain, Uncertain, "unquoted"},
		{"heredoc never ends", "cat <<'EOF'\n" + T + "\n", Uncertain, Uncertain, "never ends"},
		{"here-string", "cat <<< 'x' " + T, Uncertain, Refuse, ""},
		{"comment", "echo a # " + T, Uncertain, Uncertain, "#"},
		{"bare paren", "(echo " + T + ")", Uncertain, Uncertain, "bare ("},
		{"arith", "echo $((1+1)) " + T, Uncertain, Uncertain, "$(("},
		{"param expansion", "echo ${x} " + T, Uncertain, Uncertain, "${"},
		{"param in double quotes", "echo \"${x}\" " + T, Uncertain, Uncertain, "${"},
		{"ansi-c", "echo $'a' " + T, Uncertain, Uncertain, "$'"},
		{"case", "case x in x) " + T + ";; esac", Uncertain, Uncertain, "case"},
		{"process substitution", "cat <(echo) " + T, Uncertain, Uncertain, "<("},
		{"unclosed quote", "echo 'x " + T, Uncertain, Uncertain, "unclosed"},
		{"unclosed substitution", "echo $(echo " + T, Uncertain, Uncertain, "unclosed"},
		{"trailing backslash", "echo " + T + " \\", Uncertain, Uncertain, "backslash"},
		{"stray close", "echo ) " + T, Uncertain, Uncertain, "bare )"},
	} {
		if got := Check(tc.cmd).V; got != tc.strict {
			t.Errorf("%s: Check = %v, want %v\n  %q", tc.name, got, tc.strict, tc.cmd)
		}
		r := CheckHeredoc(tc.cmd)
		if r.V != tc.plus {
			t.Errorf("%s: CheckHeredoc = %v, want %v (%s)\n  %q", tc.name, r.V, tc.plus, r.Reason, tc.cmd)
		}
		if tc.reason != "" && tc.plus == Uncertain && !strings.Contains(r.Reason, tc.reason) {
			t.Errorf("%s: the reason %q does not name %q", tc.name, r.Reason, tc.reason)
		}
		if r.V == Uncertain && r.Reason == "" {
			t.Errorf("%s: an uncertain verdict with no named construct", tc.name)
		}
	}
}

// ⛔ THE RULE THIS PACKAGE EXISTS FOR: whatever the verdict ALLOWS must run nothing
// under the shell that executes commands here. The rows above that hold a backtick
// are asked of the real zsh, and S's of bash too.
func TestAllowedRowsRunNothing(t *testing.T) {
	zshPath(t)
	for _, cmd := range []string{
		"echo '" + T + "'",
		"echo \\`x\\`",
		"echo \"$(echo '" + T + "')\"",
		"x=\"$(cat <<'EOF'\n" + T + "\nEOF\n)\"",
		"cat <<-'EOF'\n\t" + T + "\n\tEOF\n",
	} {
		if CheckHeredoc(cmd).V != Allow {
			t.Fatalf("not an allowed row: %q", cmd)
		}
		if runs(t, "zsh", cmd) {
			t.Errorf("zsh ran a backtick S+ allowed: %q", cmd)
		}
	}
	// And the control: a refused row really does run, so the oracle can see.
	if !runs(t, "zsh", T) {
		t.Error("zsh did not run the bare control: this oracle cannot see what it is for")
	}
}

// The grammar of the fuzz and of the random in-grammar generator. Programs built
// from it mostly land OUTSIDE the grammar (refused unrun), so a second generator
// below mostly stays inside, where the shell is actually asked.
var tokens = []string{
	"echo ", ": ", "true ", "case x in x) ", ";; esac", "$(", ")", "$((", "))",
	"${x:-", "}", "'", "\"", "$'", "\\", "#", ";", "|", "&&", "\n",
	"<<'E'\n", "<<E\n", "<<-E\n", "E\n", "\tE\n", " ", "x", "(", "(( ",
	T, T, T,
	"[", "]", "{", "}", "=", "$x", "!", "~", "^", "*", "?", "<<<", "<<\\E\n", "<<-'E'\n",
	"<<\"E\"\n", "E)", "\t", "$#", "$?", "esac", "in", "$\"", "&", ">", "<",
}

func program(data []byte) string {
	var b strings.Builder
	for _, c := range data {
		b.WriteString(tokens[int(c)%len(tokens)])
	}
	return b.String()
}

func tokenSeed(want ...string) []byte {
	var out []byte
	for _, w := range want {
		for i, tok := range tokens {
			if tok == w {
				out = append(out, byte(i))
				break
			}
		}
	}
	return out
}

func fuzzSeeds(f *testing.F) {
	f.Add(tokenSeed("echo ", "'", T, "'"))
	f.Add(tokenSeed("echo ", "\"", "$(", "echo ", "'", T, "'", ")", "\""))
	f.Add(tokenSeed("cat ", "<<'E'\n", T, "\n", "E\n"))
	f.Add(tokenSeed("echo ", "$(", "<<'E'\n", "\"", "E\n", ")", "\"", "case x in x) ", "\"", ";", "#", T, "\""))
	f.Add(tokenSeed(" ", "$(", "true ", ";", "}", "x", ")", "(", "|", "E\n", "#", T))
	f.Add(tokenSeed("echo ", "\\", T))
}

// FuzzSplus: whatever CheckHeredoc allows, the production zsh must not run.
func FuzzSplus(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 40 {
			t.Skip()
		}
		cmd := program(data)
		if !strings.Contains(cmd, "`") || CheckHeredoc(cmd).V != Allow {
			return
		}
		if runs(t, "zsh", cmd) {
			t.Fatalf("zsh RAN a backtick S+ allowed: %q", cmd)
		}
	})
}

// FuzzS: whatever the strict grammar allows, neither the production zsh nor bash
// may run.
func FuzzS(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 40 {
			t.Skip()
		}
		cmd := program(data)
		if !strings.Contains(cmd, "`") || Check(cmd).V != Allow {
			return
		}
		for _, sh := range []string{"zsh", "bash"} {
			if runs(t, sh, cmd) {
				t.Fatalf("%s RAN a backtick S allowed: %q", sh, cmd)
			}
		}
	})
}

var insideTokens = []string{
	"echo ", ": ", "true ", "x", " ", "'", "\"", "\\", "$(", ")", ";", "|", "&&", "\n",
	"<<'E'\n", "<<-'E'\n", "<<\\E\n", "<<\"E\"\n", "E\n", "\tE\n", "E)", " E\n",
	"$x", "$?", "{", "}", "[", "]", "=", "*", "!", "<<<", ">", "<", "e", "s", "a",
	T, T, T, "`",
}

// insideRun draws random programs from a grammar that mostly stays inside S+,
// keeps those the verdict ALLOWS with a backtick, and asks the shells.
func insideRun(t *testing.T, verdict func(string) Result, shells []string, want int) {
	r := rand.New(rand.NewSource(1))
	judged := 0
	for i := 0; judged < want && i < 3000*want; i++ {
		var b strings.Builder
		for k := r.Intn(14) + 2; k > 0; k-- {
			b.WriteString(insideTokens[r.Intn(len(insideTokens))])
		}
		cmd := b.String()
		if !strings.Contains(cmd, "`") || verdict(cmd).V != Allow {
			continue
		}
		judged++
		for _, sh := range shells {
			if runs(t, sh, cmd) {
				t.Errorf("%s RAN a backtick the verdict allowed: %q", sh, cmd)
			}
		}
	}
	if judged < want {
		t.Errorf("only %d of %d programs were allowed with a backtick: the generator left the grammar", judged, want)
	}
	t.Logf("%d allowed programs with a backtick, asked of %v", judged, shells)
}

func TestInsideSplusUnderTheProductionZsh(t *testing.T) {
	zshPath(t)
	insideRun(t, CheckHeredoc, []string{"zsh"}, 1000)
}

func TestInsideStrictUnderZshAndBash(t *testing.T) {
	zshPath(t)
	insideRun(t, Check, []string{"zsh", "bash"}, 1000)
}
