package cliflags

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four-spelling check moved to helparg, with its tests: see the package
// comment there for the 365 KB of cobra that reached gitpush while it lived
// in this package.

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
	// ⛔⛔ A COBRA COMMAND PASSES EVERY CHECK BELOW WITHOUT ANSWERING ANY OF
	// THEM: no flag.ContinueOnError, no `.Parse(args); err != nil`, so the gate
	// would go quiet about precisely the commands being migrated -- the same
	// silence hookcheck's corpus kept about the volatile rule (#64).
	//
	// What a cobra command owes is different, and it is two things at once:
	// help exits 0 by construction (command.go:1148), but `-help` does NOT
	// (command.go:921 refuses the parse first), and every single-dash long
	// spelling this repository has ever documented is read as a cluster of
	// shorthands. [Run] is what answers both, so naming it is the requirement.
	if strings.Contains(src, "spf13/cobra") {
		if !strings.Contains(src, "cliflags.Run(") && !strings.Contains(src, "cliflags.LegacyArgs(") {
			return "cobra without cliflags.Run: `-help` exits 2, and `-since`/`-public` " +
				"are read as clusters of shorthands"
		}
		return ""
	}

	for _, shape := range []string{".Parse(args); err != nil", ".Parse(flags); err != nil"} {
		if i := strings.Index(src, shape); i >= 0 {
			// ⛔ TWELVE LINES, not four: the first window missed ghnew, whose
			// help branch sits under a four-line comment saying why it is
			// there. A window tuned to code with no comments in it refuses
			// precisely the sites somebody explained.
			under := firstLines(src[i+len(shape):], 12)
			if !strings.Contains(under, "ErrHelp") {
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
		{"an error-returning form that answers ErrHelp", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif err := fs.Parse(args); err != nil {\n\tif errors.Is(err, flag.ErrHelp) {\n\t\treturn nil\n\t}\n\treturn err\n}", false},
		// ⛔ The shape as ghnew actually carries it: the help branch is four
		// comment lines below the refused line, which is what a window of four
		// could not see.
		{"the same, explained first", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nif err := fs.Parse(args); err != nil {\n\t// one\n\t// two\n\t// three\n\t// four\n\tif errors.Is(err, flag.ErrHelp) {\n\t\treturn nil\n\t}\n\treturn err\n}", false},
		{"a hand-written ErrHelp switch", "fs := flag.NewFlagSet(\"x\", flag.ContinueOnError)\nswitch err := fs.Parse(args); {\ncase errors.Is(err, flag.ErrHelp):\n\treturn 0\n}", false},
		{"no flag set at all, asking by hand", "if cliflags.IsHelpRequest(args[0]) {\n\treturn 0\n}", false},
		// ⛔ The cobra half, both directions. The first of these is what every
		// migrated command would look like if the single-dash spellings were
		// forgotten -- green tests, working `-h`, and `ghnew -public` quietly
		// reading as the shorthands p, u, b, l, i, c.
		{"cobra with nothing in front of it", "import \"github.com/spf13/cobra\"\nfunc main() { osExit(cliflags.Run2(RootCmd(), os.Args[1:])) }", true},
		{"cobra driven through Run", "import \"github.com/spf13/cobra\"\nfunc main() { osExit(cliflags.Run(RootCmd(), os.Args[1:])) }", false},
		{"cobra calling the adapter itself", "import \"github.com/spf13/cobra\"\ncmd.SetArgs(cliflags.LegacyArgs(cmd, args))", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := dropsTheHelpDistinction(tc.src)
			if (why != "") != tc.wrong {
				t.Errorf("dropsTheHelpDistinction = %q, want wrong=%v", why, tc.wrong)
			}
		})
	}
}
