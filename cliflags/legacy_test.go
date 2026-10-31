package cliflags

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// probe builds a command with one flag of every shape these commands actually
// use: a long name, a long name with a dash in it, one with a shorthand, one
// that takes a value, and a subcommand with a flag of its own.
func probe() *cobra.Command {
	root := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
	root.Flags().Bool("public", false, "")
	root.Flags().Bool("dry-run", false, "")
	root.Flags().String("since", "", "")
	root.Flags().StringP("identities", "i", "", "")
	root.Flags().String("to", "", "")
	root.PersistentFlags().String("host", "", "")

	sub := &cobra.Command{Use: "delete", RunE: func(*cobra.Command, []string) error { return nil }}
	sub.Flags().Bool("yes", false, "")
	root.AddCommand(sub)
	return root
}

// TestEveryLongFlagStillTakesOneDash is the gate, and it ENUMERATES: it asks
// the command for its flags rather than repeating a list of them here, so a
// flag added tomorrow is covered without anybody remembering to add it.
//
// ⛔ The fleet's other answer to this problem is a hand-written map of
// seventeen names (gopin, go-fleettools PR #142). This is the same test that
// map cannot pass once an eighteenth flag exists.
func TestEveryLongFlagStillTakesOneDash(t *testing.T) {
	cmd := probe()
	cmd.InitDefaultHelpFlag()

	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if len(f.Name) > 1 {
			names = append(names, f.Name)
		}
	})
	if len(names) < 6 {
		t.Fatalf("enumerated only %d long flags (%v): this test cannot see what it is for", len(names), names)
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			c := probe()
			c.InitDefaultHelpFlag()
			args := legacyFor(c, name)
			if err := c.Flags().Parse(LegacyArgs(c, args)); err != nil {
				t.Fatalf("%q after LegacyArgs: %v", args, err)
			}
			if !c.Flags().Changed(name) {
				t.Errorf("%q parsed but did not set %s", args, name)
			}
		})
	}

	// ⛔ A GATE NOBODY HAS SEEN FAIL IS NOT A GATE. Without the rewrite, none of
	// these may arrive intact -- and the control is written as "not intact"
	// rather than "fails", because of what it found the first time it ran:
	//
	// ⛔⛔ MEASURED, 2026-10-31: `-identities ids.txt` DOES NOT FAIL under
	// pflag. `i` is a declared shorthand, so pflag@v1.0.9 flag.go:1038 reads
	// the argument as `-i` and takes THE REST OF THE CLUSTER as its value:
	// identities = "dentities", with ids.txt left over as an operand. Any long
	// flag whose first letter is some flag's shorthand migrates to cobra by
	// quietly changing its value, not by refusing it. reauthor, which rewrites
	// the author of every commit it is pointed at, has exactly that flag.
	var survived []string
	for _, name := range names {
		c := probe()
		c.InitDefaultHelpFlag()
		args := legacyFor(c, name)
		if err := c.Flags().Parse(args); err != nil {
			continue // refused, which is the loud half
		}
		if f := c.Flags().Lookup(name); f.Changed && f.Value.String() == valueIn(args) {
			survived = append(survived, name)
		}
	}
	if len(survived) > 0 {
		t.Errorf("%v arrived intact with ONE dash even without LegacyArgs: pflag changed, "+
			"or this control is measuring nothing", survived)
	}
}

// valueIn returns what legacyFor asked the flag to be set to.
func valueIn(args []string) string {
	if len(args) == 2 {
		return args[1]
	}
	return "true"
}

// legacyFor spells one flag the way package flag took it, with a value when
// the flag needs one.
func legacyFor(cmd *cobra.Command, name string) []string {
	f := cmd.Flags().Lookup(name)
	if f.NoOptDefVal != "" {
		return []string{"-" + name}
	}
	return []string{"-" + name, "VALUE"}
}

func TestLegacyArgsLeavesEverythingElseAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"the spelling that used to work", []string{"-since", "v1"}, []string{"--since", "v1"}},
		{"with its value attached", []string{"-since=v1"}, []string{"--since=v1"}},
		{"a dash in the flag's own name", []string{"-dry-run"}, []string{"--dry-run"}},
		{"the double-dash form is already right", []string{"--since", "v1"}, []string{"--since", "v1"}},
		{"a shorthand stays a shorthand", []string{"-i", "ids.txt"}, []string{"-i", "ids.txt"}},
		{"a cluster of shorthands is not a long flag", []string{"-rf"}, []string{"-rf"}},
		{"an undeclared long name is cobra's error to report", []string{"-nope"}, []string{"-nope"}},
		{"a bare dash is an operand", []string{"-"}, []string{"-"}},
		{"operands are untouched", []string{"owner/repo"}, []string{"owner/repo"}},
		{"a persistent flag counts", []string{"-host", "gitlab.example"}, []string{"--host", "gitlab.example"}},
		{"help, which cobra declares late", []string{"-help"}, []string{"--help"}},
		// ⛔ THE VALUE OF A FLAG IS NOT A FLAG. Rewriting this one would not
		// fail -- pflag takes the next argument whatever it looks like -- it
		// would hand the command a value one character different from the one
		// that was typed.
		{"a value that looks like a flag", []string{"-to", "-identities"}, []string{"--to", "-identities"}},
		{"the same for the double-dash spelling", []string{"--to", "-identities"}, []string{"--to", "-identities"}},
		// A bool takes no value, so what follows it IS a flag again.
		{"after a flag that needs no value", []string{"-public", "-since", "v1"}, []string{"--public", "--since", "v1"}},
		{"everything after a bare -- is an operand", []string{"--", "-since", "-help"}, []string{"--", "-since", "-help"}},
		{"a subcommand's own flag", []string{"delete", "-yes"}, []string{"delete", "--yes"}},
		{"a flag the subcommand does not have", []string{"delete", "-public"}, []string{"delete", "-public"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := LegacyArgs(probe(), tc.args)
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Errorf("LegacyArgs(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestLegacyArgsSurvivesANilCommand(t *testing.T) {
	args := []string{"-since", "v1"}
	if got := LegacyArgs(nil, args); strings.Join(got, " ") != "-since v1" {
		t.Errorf("LegacyArgs(nil, %q) = %q", args, got)
	}
}

// TestRunSeparatesAskedFromFailedFromMisused is the exit-code contract. The
// four help spellings are here because they are the ones people type, and
// `-help` is the one cobra alone gets wrong.
func TestRunSeparatesAskedFromFailedFromMisused(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		// ⛔ The Args validator is part of each case, because it is what makes
		// an argument a MISUSE rather than an operand: `explode` is an unknown
		// verb to a command that takes none, and an ordinary argument to one
		// that does. Writing one validator for the whole table had this test
		// claiming cobra exits 0 on an unknown verb, which was my table's
		// doing and not cobra's.
		accepts cobra.PositionalArgs
	}{
		{"it did the work", []string{"-since", "v1"}, 0, cobra.MaximumNArgs(2)},
		{"asked, short", []string{"-h"}, 0, cobra.MaximumNArgs(2)},
		{"asked, long", []string{"--help"}, 0, cobra.MaximumNArgs(2)},
		{"asked, the spelling cobra gets wrong", []string{"-help"}, 0, cobra.MaximumNArgs(2)},
		{"asked, as a verb", []string{"help"}, 0, cobra.MaximumNArgs(2)},
		{"an unknown flag is a misuse", []string{"-nope"}, 2, cobra.MaximumNArgs(2)},
		{"an unknown verb, where none is allowed", []string{"explode"}, 2, cobra.NoArgs},
		{"an operand, where they are", []string{"explode"}, 0, cobra.MaximumNArgs(2)},
		{"a missing value likewise", []string{"-since"}, 2, cobra.MaximumNArgs(2)},
		{"too many operands", []string{"a", "b", "c"}, 2, cobra.MaximumNArgs(2)},
		{"the work itself failing is not a misuse", []string{"-since", "boom"}, 1, cobra.MaximumNArgs(2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := probe()
			cmd.Args = tc.accepts
			cmd.RunE = func(c *cobra.Command, _ []string) error {
				if v, _ := c.Flags().GetString("since"); v == "boom" {
					return errors.New("the work failed")
				}
				return nil
			}
			var out, errb strings.Builder
			cmd.SetOut(&out)
			cmd.SetErr(&errb)
			if code := Run(cmd, tc.args); code != tc.code {
				t.Errorf("Run(%q) = %d, want %d\nstdout: %s\nstderr: %s",
					tc.args, code, tc.code, out.String(), errb.String())
			}
		})
	}
}

// ⭐ The usage TEXT still has to come out, on stdout, the way a question
// deserves an answer rather than a silence with a zero status.
func TestRunPrintsTheFlagsWhenAsked(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help"} {
		cmd := probe()
		var out, errb strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if code := Run(cmd, []string{arg}); code != 0 {
			t.Fatalf("Run(%q) = %d", arg, code)
		}
		if !strings.Contains(out.String(), "--since") {
			t.Errorf("%s: the usage text does not name the flags:\nstdout: %s\nstderr: %s",
				arg, out.String(), errb.String())
		}
	}
}

// ⛔ A misuse must SAY so on stderr, and must not be mistaken for help: the
// defect this whole package was written for was a command that printed its
// usage and exited 2, which reads as "you are wrong" to a person and as
// "it broke" to a script.
func TestRunNamesTheCommandOnAMisuse(t *testing.T) {
	cmd := probe()
	var out, errb strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if code := Run(cmd, []string{"-nope"}); code != 2 {
		t.Fatalf("Run = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "probe:") {
		t.Errorf("the error does not name the command:\n%s", errb.String())
	}
	if !strings.Contains(errb.String(), "--help") {
		t.Errorf("the error does not say how to ask:\n%s", errb.String())
	}
}
