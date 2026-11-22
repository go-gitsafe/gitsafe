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

	"github.com/go-gitsafe/gitsafe/amendmerged"
	"github.com/go-gitsafe/gitsafe/bypass"
	"github.com/go-gitsafe/gitsafe/chainedgate"
	"github.com/go-gitsafe/gitsafe/cliflags"
	"github.com/go-gitsafe/gitsafe/discard"
	"github.com/go-gitsafe/gitsafe/eqword"
	"github.com/go-gitsafe/gitsafe/ghbyhand"
	"github.com/go-gitsafe/gitsafe/literaltick"
	"github.com/go-gitsafe/gitsafe/pipestatus"
	"github.com/go-gitsafe/gitsafe/redact"
	"github.com/go-gitsafe/gitsafe/secretarg"
	"github.com/go-gitsafe/gitsafe/shellwork"
	"github.com/go-gitsafe/gitsafe/sigpipe"
	"github.com/go-gitsafe/gitsafe/stalebase"
	"github.com/go-gitsafe/gitsafe/testrun"
	"github.com/go-gitsafe/gitsafe/volatile"
	"github.com/go-gitsafe/gitsafe/waiveenv"
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
		why      bool
		full     bool
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
			if code := report(cmd, dir, rule, escaped, examples, why, full); code != 0 {
				return cliflags.Status(code)
			}
			return nil
		},
	}
	home, _ := os.UserHomeDir()
	c.Flags().StringVar(&dir, "transcripts", filepath.Join(home, ".claude", "projects"),
		"directory of Claude Code transcripts to read")
	c.Flags().StringVar(&rule, "rule", "", "only this rule (bypass, secretarg, discard, pipestatus, sigpipe, literaltick, literaltick-legacy, ghbyhand, waiveenv, testrun, chainedgate, shellwork, stalebase, amendmerged, volatile, and the counting-only eqword and eqword-intended)")
	c.Flags().BoolVar(&escaped, "escaped", false,
		"only the commands somebody ran with a GITSAFE_ALLOW_* prefix")
	c.Flags().IntVar(&examples, "examples", 3, "distinct commands to show per refusal kind")
	c.Flags().BoolVar(&why, "why", false,
		"under each example, what the rule says it matched -- the long command's "+
			"prefix is not where the match is")
	c.Flags().BoolVar(&full, "full", false,
		"print each example command whole, newlines and all, instead of one "+
			"truncated line -- a reconstruction of a long command is not the command")
	return c
}

// countingOnly are the rows that count and that guard-bash does not run: their
// hits are reported on their own line and never in "would be refused".
var countingOnly = map[string]bool{"eqword": true, "eqword-intended": true, "literaltick-legacy": true}

// rules is every rule guard-bash runs, adapted to one shape.
//
// ⛔ THE LIST IS THE RISK HERE, and it is the same risk hookcheck's corpus had:
// a rule nobody adds a line for is a rule this scan stays quiet about. The
// test TestEveryRuleGuardBashRunsIsScanned reads the import block of
// cmd/guard-bash/main.go and fails when this map does not name all of them.
// refusal is one rule's answer about one command: the group it belongs to, and
// what the rule actually matched inside it.
//
// ⛔ WHY THE MATCHED TEXT IS CARRIED AT ALL. A count alone is not actionable,
// and worse, it invites a diagnosis from the command's PREFIX — which is all
// the example lines can show for a long command. That is not a hypothetical:
// reading truncated examples of this rule's 5 050 refusals, the first
// conclusion drawn was that they were dominated by `gh pr create --body
// "$(cat <<'EOF' …)"`, and a test written against that shape passed. The
// backtick was elsewhere in the line every time. --why prints what the rule
// says it matched, so the shape is read from the rule rather than guessed.
type refusal struct {
	Kind  string // the group label, one line per kind in the report
	Why   string // what the rule matched, when the rule can say
	Found bool
}

func rules(cwd, home, tmpdir string) map[string]func(string) refusal {
	// kind takes the finding itself so that any rule with a String method
	// contributes its own account of the match; the rest report the kind only.
	kind := func(f any, found bool, k string) refusal {
		if !found {
			return refusal{}
		}
		if k == "" {
			k = "refused"
		}
		r := refusal{Kind: k, Found: true}
		if s, ok := f.(fmt.Stringer); ok {
			r.Why = s.String()
		}
		return r
	}
	return map[string]func(string) refusal{
		"bypass": func(c string) refusal {
			f := bypass.Check(c)
			return kind(f, f.Found(), f.Rule)
		},
		"secretarg": func(c string) refusal {
			f := secretarg.Check(c)
			return kind(f, f.Found(), f.Rule)
		},
		"discard": func(c string) refusal {
			f := discard.Check(c)
			return kind(f, f.Found(), f.Rule)
		},
		"pipestatus": func(c string) refusal {
			f := pipestatus.Check(c)
			return kind(f, f.Found(), "a status read from the wrong command")
		},
		"sigpipe": func(c string) refusal {
			f := sigpipe.Check(c)
			return kind(f, f.Found(), "a producer killed by its consumer")
		},
		// ⛔ IN HERE BECAUSE main_test.go REFUSED THE RULE WITHOUT IT, and that
		// refusal is the repository working: a rule guard-bash runs and
		// guardscan cannot scan is a rule whose false refusals nobody would
		// ever count. The act it names is the fleet program, so the kind is
		// what to run instead.
		"waiveenv": func(c string) refusal {
			f := waiveenv.Check(c)
			return kind(f, f.Found(), "a waiver or skip variable: "+f.Var)
		},
		"ghbyhand": func(c string) refusal {
			f := ghbyhand.Check(c)
			return kind(f, f.Found(), f.Act)
		},
		// Same gate, same reason — and this one's false refusals are the ones
		// to count, since it refuses a character rather than a command shape.
		// What guard-bash runs since #156: S+ for zsh, the shell that executes these
		// commands. The refusal kind is the construct that put a command outside
		// the grammar, so a refusal-rate delta can be read per construct.
		"literaltick": func(c string) refusal {
			f := literaltick.CheckFor("zsh", c)
			label := "a backtick the shell would evaluate"
			if f.Construct != "" {
				label = "outside the grammar: " + f.Construct
			}
			return kind(f, f.Found(), label)
		},
		// COUNTING ONLY: the reading guard-bash ran before #156, for the before side
		// of the refusal-rate delta.
		"literaltick-legacy": func(c string) refusal {
			f := literaltick.Check(c)
			return kind(f, f.Found(), "a backtick the shell would evaluate (bash 3.2 reading)")
		},
		// ⛔ COUNTING ONLY (go-gitsafe/gitsafe#129): guard-bash does not run
		// eqword. These two rows say how often a word zsh equals-expands would
		// take the rest of a line with it ("eqword"), and how often somebody
		// wrote =cmd on purpose and it worked ("eqword-intended"), so that the
		// refuse / warn / nothing decision is made from the record.
		"eqword": func(c string) refusal {
			if hs := eqword.Check(c); len(hs) > 0 {
				return kind(hs[0], true, hs[0].Kind())
			}
			return refusal{}
		},
		"eqword-intended": func(c string) refusal {
			for _, h := range eqword.Scan(c) {
				if h.Resolves {
					return kind(h, true, h.Kind())
				}
			}
			return refusal{}
		},
		"shellwork": func(c string) refusal {
			f := shellwork.Check(c)
			return kind(f, f.Found(), f.Kind)
		},
		"testrun": func(c string) refusal {
			f := testrun.Check(c)
			return kind(f, f.Found(), f.Kind)
		},
		"chainedgate": func(c string) refusal {
			f := chainedgate.Check(c)
			return kind(f, f.Found(), f.Gate+" chained with "+f.Write)
		},
		"stalebase": func(c string) refusal {
			f := stalebase.Check(c)
			return kind(f, f.Found(), "a branch started from an unrefreshed remote ref")
		},
		// This rule asks git, so the count is against the scan's own directory,
		// not the clone each old command ran in: read it as "commands that carry
		// an amend" with a refusal only where that directory's HEAD is published.
		"amendmerged": func(c string) refusal {
			f := amendmerged.Check(c, cwd, home)
			return kind(f, f.Found(), "an amend of a commit already on the default branch")
		},
		"volatile": func(c string) refusal {
			f := volatile.Check(c, cwd, home, tmpdir)
			if f.Found() {
				return kind(f, true, f.Rule)
			}
			// The transcripts do not record run_in_background, so this counts
			// the commands the redirect half WOULD refuse if they had been
			// started in the background: an upper bound, which is what a
			// false-refusal hunt wants.
			g := volatile.CheckRedirect(c, cwd, home, tmpdir, true)
			return kind(g, g.Found(), "output redirected to a volatile directory (if backgrounded)")
		},
	}
}

type group struct {
	rule, kind string
	commands   map[string]int
	whys       map[string]string // command -> what the rule matched in it
	total      int
}

func report(cmd *cobra.Command, dir, only string, escapedOnly bool, examples int, why, full bool) int {
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
		checks = map[string]func(string) refusal{only: fn}
	}

	groups := map[string]*group{}
	refusedTotal, countedTotal := 0, 0
	for command, n := range corpus.Commands {
		if escapedOnly {
			if _, ok := escapePrefix(command); !ok {
				continue
			}
		}
		for name, check := range checks {
			r := check(command)
			if !r.Found {
				continue
			}
			key := name + "\x00" + r.Kind
			g := groups[key]
			if g == nil {
				g = &group{rule: name, kind: r.Kind, commands: map[string]int{}, whys: map[string]string{}}
				groups[key] = g
			}
			g.whys[command] = r.Why
			g.commands[command] += n
			g.total += n
			if countingOnly[name] {
				countedTotal += n // a count is not a refusal
			} else {
				refusedTotal += n
			}
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
	if countedTotal > 0 {
		fmt.Fprintf(stdout, "counted only (no rule refuses these): %d of %d command(s)\n",
			countedTotal, corpus.Total)
	}

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
			if full {
				fmt.Fprintf(stdout, "    ----8<----\n%s\n    ---->8----\n", r.String(command))
			} else {
				fmt.Fprintf(stdout, "    %s\n", oneLine(r.String(command)))
			}
			if !why {
				continue
			}
			if w := g.whys[command]; w != "" {
				fmt.Fprintf(stdout, "      ↳ %s\n", oneLine(r.String(w)))
			} else {
				fmt.Fprintf(stdout, "      ↳ (this rule does not say what it matched)\n")
			}
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
