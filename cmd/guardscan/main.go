// guardscan puts the commands this machine actually runs to every rule
// guard-bash enforces, and says what each rule would refuse.
//
// It exists because a false refusal is found, today, one at a time, by whoever
// trips over it mid-task — and then usually worked around rather than reported.
// On 2026-10-31 two of them were found that way in one session, one of which
// had been sitting in a note since the rule shipped. A rule that refuses
// legitimate work teaches people to stop trusting the guard, so the question
// "what does this refuse that it should not?" deserves to be asked of the
// whole record rather than of the last thing that annoyed somebody.
//
//	guardscan                        every transcript under ~/.claude/projects
//	guardscan --rule shellwork       one rule
//	guardscan --escaped              only where somebody reached for the escape
//
// ⛔ IT RUNS NOTHING. Every rule is a pure function of the command text.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-gitsafe/gitsafe/bypass"
	"github.com/go-gitsafe/gitsafe/cliflags"
	"github.com/go-gitsafe/gitsafe/discard"
	"github.com/go-gitsafe/gitsafe/pipestatus"
	"github.com/go-gitsafe/gitsafe/redact"
	"github.com/go-gitsafe/gitsafe/secretarg"
	"github.com/go-gitsafe/gitsafe/shellwork"
	"github.com/go-gitsafe/gitsafe/sigpipe"
	"github.com/go-gitsafe/gitsafe/volatile"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// maxLine is the longest transcript line this will read. A pasted file or a
// large tool result makes lines of hundreds of kilobytes.
const maxLine = 8 << 20

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	cmd := RootCmd()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cliflags.Run(cmd, args)
}

// RootCmd returns the command, for tests to drive with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var (
		dir      string
		rule     string
		escaped  bool
		examples int
	)
	c := &cobra.Command{
		Use:   "guardscan",
		Short: "Say what the Bash guard would refuse, over the commands this machine really runs",
		Long: `guardscan reads the Claude Code transcripts and puts every Bash command in
them to each rule guard-bash enforces, so that a refusal of legitimate work is
found by asking the whole record rather than by tripping over it.

It runs nothing: every rule is a pure function of the command text. Commands
are printed through the redactor, the same way the guards print.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if code := report(cmd, dir, rule, escaped, examples); code != 0 {
				return cliflags.Status(code)
			}
			return nil
		},
	}
	home, _ := os.UserHomeDir()
	c.Flags().StringVar(&dir, "transcripts", filepath.Join(home, ".claude", "projects"),
		"directory of Claude Code transcripts to read")
	c.Flags().StringVar(&rule, "rule", "", "only this rule (bypass, secretarg, discard, pipestatus, sigpipe, shellwork, volatile)")
	c.Flags().BoolVar(&escaped, "escaped", false,
		"only the commands somebody ran with a GITSAFE_ALLOW_* prefix")
	c.Flags().IntVar(&examples, "examples", 3, "distinct commands to show per refusal kind")
	return c
}

// rules is every rule guard-bash runs, adapted to one shape.
//
// ⛔ THE LIST IS THE RISK HERE, and it is the same risk hookcheck's corpus had:
// a rule nobody adds a line for is a rule this scan stays quiet about. The
// test TestEveryRuleGuardBashRunsIsScanned reads the import block of
// cmd/guard-bash/main.go and fails when this map does not name all of them.
func rules(cwd, home, tmpdir string) map[string]func(string) (string, bool) {
	kind := func(found bool, k string) (string, bool) {
		if !found {
			return "", false
		}
		if k == "" {
			k = "refused"
		}
		return k, true
	}
	return map[string]func(string) (string, bool){
		"bypass": func(c string) (string, bool) {
			f := bypass.Check(c)
			return kind(f.Found(), f.Rule)
		},
		"secretarg": func(c string) (string, bool) {
			f := secretarg.Check(c)
			return kind(f.Found(), f.Rule)
		},
		"discard": func(c string) (string, bool) {
			f := discard.Check(c)
			return kind(f.Found(), f.Rule)
		},
		"pipestatus": func(c string) (string, bool) {
			f := pipestatus.Check(c)
			return kind(f.Found(), "a status read from the wrong command")
		},
		"sigpipe": func(c string) (string, bool) {
			f := sigpipe.Check(c)
			return kind(f.Found(), "a producer killed by its consumer")
		},
		"shellwork": func(c string) (string, bool) {
			f := shellwork.Check(c)
			return kind(f.Found(), f.Kind)
		},
		"volatile": func(c string) (string, bool) {
			f := volatile.Check(c, cwd, home, tmpdir)
			return kind(f.Found(), f.Rule)
		},
	}
}

type group struct {
	rule, kind string
	commands   map[string]int
	total      int
}

func report(cmd *cobra.Command, dir, only string, escapedOnly bool, examples int) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()

	corpus, err := Read(dir, maxLine)
	if err != nil {
		fmt.Fprintf(stderr, "guardscan: %v\n", err)
		return 1
	}

	// ⛔ A SCAN THAT COULD NOT READ MUST NOT REPORT A CLEAN MACHINE. Zero
	// commands means the directory, the format or the permissions are wrong,
	// and "no false refusals" is exactly what that looks like from outside.
	if corpus.Files == 0 {
		fmt.Fprintf(stderr, "guardscan: no transcripts under %s: nothing was measured\n", dir)
		return 1
	}
	if corpus.Total == 0 {
		fmt.Fprintf(stderr, "guardscan: read %d file(s) and %d line(s) but found no Bash command: "+
			"the transcript format is not the one this reads, and a silent zero here "+
			"would read as a guard that refuses nothing\n", corpus.Files, corpus.Lines)
		return 1
	}

	r := redact.New()
	checks := rules(dir, os.Getenv("HOME"), os.Getenv("TMPDIR"))
	if only != "" {
		fn, ok := checks[only]
		if !ok {
			fmt.Fprintf(stderr, "guardscan: no rule called %q\n", only)
			return 2
		}
		checks = map[string]func(string) (string, bool){only: fn}
	}

	groups := map[string]*group{}
	refusedTotal := 0
	for command, n := range corpus.Commands {
		if escapedOnly {
			if _, ok := escapePrefix(command); !ok {
				continue
			}
		}
		for name, check := range checks {
			k, found := check(command)
			if !found {
				continue
			}
			key := name + "\x00" + k
			g := groups[key]
			if g == nil {
				g = &group{rule: name, kind: k, commands: map[string]int{}}
				groups[key] = g
			}
			g.commands[command] += n
			g.total += n
			refusedTotal += n
		}
	}

	fmt.Fprintf(stdout, "read %d transcript(s), %d line(s), %d Bash command(s), %d distinct\n",
		corpus.Files, corpus.Lines, corpus.Total, len(corpus.Commands))
	if len(corpus.Escaped) > 0 {
		fmt.Fprintf(stdout, "\n⭐ where somebody reached for the escape — each one is a refusal "+
			"they judged wrong, holding the problem:\n")
		for _, name := range sortedKeys(corpus.Escaped) {
			fmt.Fprintf(stdout, "  %-28s %d\n", name, corpus.Escaped[name])
		}
	}
	fmt.Fprintf(stdout, "\nwould be refused: %d of %d command(s)\n", refusedTotal, corpus.Total)

	ordered := make([]*group, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].total != ordered[j].total {
			return ordered[i].total > ordered[j].total
		}
		return ordered[i].rule+ordered[i].kind < ordered[j].rule+ordered[j].kind
	})

	for _, g := range ordered {
		fmt.Fprintf(stdout, "\n%s / %s — %d run(s), %d distinct\n", g.rule, g.kind, g.total, len(g.commands))
		for i, command := range sortedKeys(g.commands) {
			if i >= examples {
				break
			}
			fmt.Fprintf(stdout, "    %s\n", oneLine(r.String(command)))
		}
	}
	return 0
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// oneLine keeps a multi-line command readable in a table without hiding that
// it had more to it.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ; ")), " ")
	if len(s) > 160 {
		return s[:157] + "..."
	}
	return s
}
