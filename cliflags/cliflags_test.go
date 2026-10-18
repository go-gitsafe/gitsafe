package cliflags

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseFlagsSeparatesAQuestionFromAMistake drives all three outcomes. The
// help one is why this package exists, and it is the one no other test in this
// repository ever reached: all ten commands had it wrong.
func TestParseFlagsSeparatesAQuestionFromAMistake(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		ok   bool
	}{
		{"a help request is answered, not refused", []string{"-h"}, 0, false},
		{"--help likewise", []string{"--help"}, 0, false},
		{"an unknown flag IS a usage error", []string{"-nope"}, 2, false},
		{"a bad value for a known flag too", []string{"-n", "lots"}, 2, false},
		{"nothing to complain about", []string{"-n", "3", "owner/repo"}, 0, true},
		{"no arguments at all", nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			fs.Int("n", 1, "how many")
			code, ok := ParseFlags(fs, tc.args)
			if code != tc.code || ok != tc.ok {
				t.Errorf("ParseFlags(%q) = (%d, %v), want (%d, %v)", tc.args, code, ok, tc.code, tc.ok)
			}
		})
	}
}

// ⭐ The usage TEXT still has to come out. A command answering -h with silence
// and a zero status would be worse than the 2 this replaces: a tool that looks
// like it worked and said nothing.
func TestAHelpRequestStillPrintsTheFlags(t *testing.T) {
	var out strings.Builder
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.SetOutput(&out)
	fs.Int("n", 1, "how many")
	if code, ok := ParseFlags(fs, []string{"-h"}); code != 0 || ok {
		t.Fatalf("ParseFlags = (%d, %v), want (0, false)", code, ok)
	}
	if !strings.Contains(out.String(), "-n") {
		t.Errorf("the usage text does not name the flag:\n%s", out.String())
	}
}

// IsHelpError is for ghnew, whose run() returns an error: help means nil.
func TestIsHelpErrorKnowsOnlyTheHelpError(t *testing.T) {
	if !IsHelpError(flag.ErrHelp) {
		t.Error("IsHelpError(flag.ErrHelp) = false")
	}
	if !IsHelpError(wrapped{flag.ErrHelp}) {
		t.Error("a WRAPPED ErrHelp is still a help request: errors.Is, not ==")
	}
	for _, err := range []error{nil, errors.New("flag provided but not defined: -nope"), os.ErrNotExist} {
		if IsHelpError(err) {
			t.Errorf("IsHelpError(%v) = true", err)
		}
	}
}

type wrapped struct{ err error }

func (w wrapped) Error() string { return "while parsing: " + w.err.Error() }
func (w wrapped) Unwrap() error { return w.err }

// ⛔ The two commands that asked this by hand knew TWO of the four spellings
// (gitpush) and none (ghscopes, where an unrecognised argument became a
// demanded scope). The negative half matters as much: `-help-me` is not a
// request for help, and neither is a repository called `help`.
func TestIsHelpRequestKnowsAllFourSpellings(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help", "help"} {
		if !IsHelpRequest(arg) {
			t.Errorf("IsHelpRequest(%q) = false, want true", arg)
		}
	}
	for _, arg := range []string{"", "h", "-H", "--Help", "helpme", "-help-me", "workflow", "go-helpers/help"} {
		if IsHelpRequest(arg) {
			t.Errorf("IsHelpRequest(%q) = true, want false", arg)
		}
	}
}

// TestEveryCommandAnswersHelpWithZero holds the whole repository to it.
//
// ⛔ It reads SOURCES rather than running the binaries, which is not caution
// about speed: these commands create repositories, merge pull requests, delete
// package versions and push. `ghnew -h` must not be answered by a run.
//
// ⛔ AND IT IS TEXT, SO IT HAS A BLIND SPOT, named rather than hidden: it knows
// the spelling `.Parse(` followed by a bare `err != nil`, and the per-command
// net behind it -- anything with a flag set must name one of these helpers or
// ErrHelp. A command that hand-rolls its own argument loop with no flag set at
// all (ghscopes was one) is invisible to both; the hooks and the credential
// helper, which git invokes and nobody types, are deliberately exempt.
func TestEveryCommandAnswersHelpWithZero(t *testing.T) {
	// git invokes these; `-h` is not a thing anybody types at them.
	exempt := map[string]bool{
		"git-credential-tokenfile": true,
		"git-pre-push-guard":       true,
		"git-post-checkout-guard":  true,
		"guard-bash":               true,
	}
	dirs, err := filepath.Glob(filepath.Join("..", "cmd", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var cmds int
	var broken []string
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() || exempt[filepath.Base(d)] {
			continue
		}
		srcs, err := filepath.Glob(filepath.Join(d, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		var body strings.Builder
		for _, f := range srcs {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			body.Write(b)
		}
		src := body.String()
		if !strings.Contains(src, "func main()") && !strings.Contains(src, "func main(") {
			continue
		}
		cmds++
		if why := dropsTheHelpDistinction(src); why != "" {
			broken = append(broken, filepath.Base(d)+" ("+why+")")
		}
	}
	if cmds == 0 {
		t.Fatal("found no commands at all: the glob is wrong, not the commands")
	}
	if len(broken) > 0 {
		t.Fatalf("%d of %d commands answer -h with a non-zero status; use cliflags: %v",
			len(broken), cmds, broken)
	}
}

// dropsTheHelpDistinction says why a command's source would answer -h with a
// non-zero status, or "" when it would not. Split out so the gate has a
// control: TestTheHelpGateCanFail.
func dropsTheHelpDistinction(src string) string {
	for _, shape := range []string{".Parse(args); err != nil", ".Parse(flags); err != nil"} {
		if i := strings.Index(src, shape); i >= 0 {
			// ⛔ TWELVE LINES, not four: the first window missed ghnew, whose
			// help branch sits under a four-line comment saying why it is
			// there. A window tuned to code with no comments in it refuses
			// precisely the sites somebody explained.
			under := firstLines(src[i+len(shape):], 12)
			if !strings.Contains(under, "ErrHelp") && !strings.Contains(under, "IsHelpError") {
				return "`" + shape + "` returns the same status for -h as for a typo"
			}
		}
	}
	if strings.Contains(src, "flag.ContinueOnError") &&
		!strings.Contains(src, "cliflags.") && !strings.Contains(src, "ErrHelp") {
		return "flag.ContinueOnError without cliflags or ErrHelp: -h arrives as an error"
	}
	return ""
}

func firstLines(s string, n int) string {
	for i, c := range s {
		if c == '\n' {
			n--
			if n == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// ⛔ A GATE NOBODY HAS SEEN FAIL IS NOT A GATE.
func TestTheHelpGateCanFail(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   string
		wrong bool
	}{
		{"the shape all ten were written with", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif err := fs.Parse(args); err != nil {\n\treturn 2\n}", true},
		{"the same with the other variable name", "fset := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif err := fset.Parse(flags); err != nil {\n\treturn exitUsage\n}", true},
		{"ContinueOnError and no sign of the distinction", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nfs.Parse(args)", true},
		{"the helper", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif code, ok := cliflags.ParseFlags(fs, args); !ok {\n\treturn code\n}", false},
		{"ghnew's error-returning form", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif err := fs.Parse(args); err != nil {\n\tif cliflags.IsHelpError(err) {\n\t\treturn nil\n\t}\n\treturn err\n}", false},
		// ⛔ The shape as ghnew actually carries it: the help branch is four
		// comment lines below the refused line, which is what a window of four
		// could not see.
		{"the same, explained first", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif err := fs.Parse(args); err != nil {\n\t// one\n\t// two\n\t// three\n\t// four\n\tif cliflags.IsHelpError(err) {\n\t\treturn nil\n\t}\n\treturn err\n}", false},
		{"a hand-written ErrHelp switch", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nswitch err := fs.Parse(args); {\ncase errors.Is(err, flag.ErrHelp):\n\treturn 0\n}", false},
		{"no flag set at all, asking by hand", "if cliflags.IsHelpRequest(args[0]) {\n\treturn 0\n}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := dropsTheHelpDistinction(tc.src)
			if (why != "") != tc.wrong {
				t.Errorf("dropsTheHelpDistinction = %q, want wrong=%v", why, tc.wrong)
			}
		})
	}
}
