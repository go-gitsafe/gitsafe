package eqword

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// probe is one line put to a real shell: does a command written AFTER it still
// run? Abandoned means it did not.
type probe struct {
	name string
	line string // what is under test
	tail string // what follows it; the default is "; touch M", on the same line
	// zsh is the expected verdict for zsh -f; bash and dash never abandon a
	// line over a word beginning with `=`, and the table asserts that too.
	zshAbandons bool
}

// probes is the table MEASURED on 2026-11-16 (zsh 5.9, macOS) before any rule
// was written. ⛔ The measurement came first on purpose: the issue's own
// diagnosis (a leading-= word whose lookup fails) was a claim about zsh, and
// the table is where it is checked rather than assumed.
var probes = []probe{
	{name: "separator", line: "echo ====", zshAbandons: true},
	// A lone `=` is NOT expanded (measured): the word needs a name after it.
	{name: "lone equals", line: "echo =", zshAbandons: false},
	{name: "double equals then word", line: "echo ==x", zshAbandons: true},
	{name: "existing command", line: "echo =ls", zshAbandons: false},
	{name: "missing command", line: "echo =nonexistent", zshAbandons: true},
	// ⛔ The issue called an assignment safe ("distinct from an assignment");
	// zsh disagrees: the VALUE of an assignment is expanded, so `a==b` is
	// `a==b` -> a==(=b) -> "b not found". Measured, not predicted.
	{name: "assignment with equals value", line: "a==b", zshAbandons: true},
	{name: "assignment of equals run", line: "a=====", zshAbandons: true},
	{name: "assignment before a command", line: "A==b true", zshAbandons: true},
	{name: "assignment as an argument", line: "echo numpy==2.5.4", zshAbandons: false},
	{name: "assignment after export", line: "export A==b", zshAbandons: true},
	{name: "assignment with a found value", line: "a==true", zshAbandons: false},
	{name: "bare double equals argument", line: "echo ==", zshAbandons: true},
	{name: "single bracket test with ==", line: "[ a == b ]", zshAbandons: true},
	{name: "test builtin with ==", line: "test a == b", zshAbandons: true},
	{name: "single bracket test with =", line: "[ a = b ]", zshAbandons: false},
	{name: "regex operator word", line: "echo =~", zshAbandons: true},
	{name: "double bracket regex", line: "[[ a =~ b ]]", zshAbandons: false},
	{name: "double bracket test", line: "[[ a == b ]]", zshAbandons: false},
	{name: "double bracket with equals-run operand", line: "[[ ==== == ==== ]]", zshAbandons: true},
	{name: "double bracket with equals right side", line: "[[ a == ==== ]]", zshAbandons: true},
	// A background job is forked: the error dies with the child.
	{name: "background", line: "echo ==== &", tail: "\ntouch M", zshAbandons: false},
	{name: "next line, not the same line", line: "echo ====", tail: "\ntouch M", zshAbandons: true},
	{name: "process substitution is not equals expansion", line: "cat =(echo hi) >/dev/null", zshAbandons: false},
	{name: "flag with equals inside", line: "echo --flag==x", zshAbandons: false},
	{name: "single quoted", line: "echo '===='", zshAbandons: false},
	{name: "double quoted", line: `echo "===="`, zshAbandons: false},
	{name: "backslash escaped", line: `echo \====`, zshAbandons: false},
	{name: "heredoc body", line: "cat <<EOF >/dev/null\n====\nEOF", tail: "\ntouch M", zshAbandons: false},
	{name: "command substitution", line: "echo $(echo ====)", zshAbandons: false},
	// The error kills the substitution's own subshell, not the line.
	{name: "word inside command substitution", line: "echo $(echo =nonexistent)", zshAbandons: false},
	{name: "command name missing", line: "=nonexistent", zshAbandons: true},
	{name: "command name existing", line: "=true", zshAbandons: false},
	{name: "after semicolon", line: "true; echo ====", zshAbandons: true},
	{name: "after and", line: "true && echo ====", zshAbandons: true},
	{name: "after or when skipped", line: "true || echo ====", zshAbandons: false},
	{name: "after or when run", line: "false || echo ====", zshAbandons: true},
	{name: "skipped by and", line: "false && echo ====", zshAbandons: false},
	{name: "redirect target", line: "echo x > /dev/null ====", zshAbandons: true},
	{name: "for list", line: "for i in ====; do :; done", zshAbandons: true},
	{name: "case word", line: "case ==== in *) :;; esac", zshAbandons: true},
	{name: "inside braces", line: "{ echo ====; }", zshAbandons: true},
	// A forked stage dies alone: the line survives. Only the stage the shell
	// runs ITSELF (the last of a pipeline) takes the line with it.
	{name: "inside subshell", line: "(echo ====)", zshAbandons: false},
	{name: "array assignment", line: "x=(====)", zshAbandons: true},
	{name: "first word of a pipeline", line: "echo ==== | cat", zshAbandons: false},
	{name: "second word of a pipeline", line: "echo x | echo ====", zshAbandons: true},
	{name: "no_equals option", line: "setopt no_equals; echo ====", zshAbandons: false},
	{name: "equals with a digit", line: "echo =1", zshAbandons: true},
	{name: "equals with a dash", line: "echo =-", zshAbandons: true},
}

func shellPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		if strings.Contains(os.Getenv("GITSAFE_ORACLE_SHELLS"), name) {
			t.Fatalf("GITSAFE_ORACLE_SHELLS requires %s and it is not installed", name)
		}
		t.Skipf("%s is not installed", name)
	}
	return p
}

// runs reports whether the marker was created by running line+tail.
func runs(t *testing.T, shell string, args []string, p probe) bool {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "m")
	tail := p.tail
	if tail == "" {
		tail = "; touch M"
	}
	script := p.line + strings.ReplaceAll(tail, "M", marker)
	c := exec.Command(shell, append(args, script)...)
	c.Dir = dir
	c.Stdin = nil
	_ = c.Run() // a failing command is the point; the marker is the verdict
	_, err := os.Stat(marker)
	return err == nil
}

func TestZshProbeTable(t *testing.T) {
	zsh := shellPath(t, "zsh")
	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			got := !runs(t, zsh, []string{"-f", "-c"}, p)
			if got != p.zshAbandons {
				t.Errorf("zsh -f -c %q: abandoned=%v, table says %v", p.line, got, p.zshAbandons)
			}
		})
	}
}

// TestBashAndDashNeverAbandon is the control: the loss is zsh's, and this says
// so for the two shells the oracle also runs.
func TestBashAndDashNeverAbandon(t *testing.T) {
	for _, sh := range []string{"bash", "dash"} {
		path := shellPath(t, sh)
		for _, p := range probes {
			if strings.Contains(p.line, "setopt") || strings.Contains(p.line, "[[") && sh == "dash" ||
				strings.Contains(p.line, "=(") || p.name == "no_equals option" ||
				strings.HasPrefix(p.line, "=") || strings.Contains(p.line, "x=(") {
				continue // zsh-only or bash-only syntax: not a control for this line
			}
			t.Run(sh+"/"+p.name, func(t *testing.T) {
				if !runs(t, path, []string{"-c"}, p) {
					t.Errorf("%s -c %q abandoned the line", sh, p.line)
				}
			})
		}
	}
}
