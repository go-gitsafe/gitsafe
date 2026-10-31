package cliflags

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Run drives one cobra command the way every command in this repository does,
// and is the single place that decides what to exit with.
//
//	func main() { osExit(cliflags.Run(RootCmd(), os.Args[1:])) }
//
// The three statuses are the ones these commands have always answered, and the
// distinction is the whole reason this package exists:
//
//	0  it did the work, or it was ASKED something (-h, --help, help, -help)
//	1  it tried and failed
//	2  it was MISUSED: an unknown flag, a bad value, the wrong arguments
//
// ⛔ cobra DOES NOT MAKE THAT SECOND DISTINCTION FOR US. Execute returns one
// error for both, so a usage mistake and a failed push would exit alike. The
// two places a misuse can arrive are wrapped here -- FlagErrorFunc, which
// cobra calls at command.go:921, and the command's own Args validator, whose
// errors do not go through FlagErrorFunc at all -- so that everything else is
// a failure by elimination, rather than by a guess about the message text.
func Run(cmd *cobra.Command, args []string) int {
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usage{err} })
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(c *cobra.Command, a []string) error {
			if err := validate(c, a); err != nil {
				return usage{err}
			}
			return nil
		}
	}

	// ⛔ A COMMAND WITH NO VERBS CANNOT BE ASKED ABOUT ONE. cobra adds the
	// `help` subcommand only where there are subcommands to describe
	// (command.go:1264), so for a flags-only command `help` arrives as an
	// ordinary operand -- which is exactly how ghscopes used to answer it, by
	// reading the token and reporting that a scope called `help` was missing.
	if len(args) == 1 && args[0] == "help" && !cmd.HasSubCommands() {
		args = []string{"--help"}
	}

	// One place prints, so a message cannot come out twice and the name of the
	// command is always in front of it.
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(LegacyArgs(cmd, args))

	err := cmd.Execute()
	if err == nil {
		return 0
	}
	// ⛔ A BODY THAT HAS ALREADY SPOKEN MUST NOT BE SPOKEN OVER. Every command
	// here prints its own diagnosis -- which remote carried a credential, which
	// scope is missing, which file is readable by others -- and those wordings
	// are what their tests assert and what a person reads while holding a
	// token. [Status] carries the exit code up without a second message.
	var st Status
	if errors.As(err, &st) {
		return int(st)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", cmd.Name(), err)
	var u usage
	if errors.As(err, &u) {
		fmt.Fprintf(cmd.ErrOrStderr(), "Run '%s --help' for usage.\n", cmd.CommandPath())
		return 2
	}
	return 1
}

// Status is what a command's body returns when it has already said on stderr
// what went wrong and only needs the status carried out of [Run].
//
//	RunE: func(cmd *cobra.Command, args []string) error {
//		if code := run(cmd.OutOrStdout(), cmd.ErrOrStderr(), args); code != 0 {
//			return cliflags.Status(code)
//		}
//		return nil
//	}
type Status int

func (s Status) Error() string { return fmt.Sprintf("exit status %d", int(s)) }

// usage marks an error as "you were misused", so [Run] can answer 2 without
// reading the message.
type usage struct{ err error }

func (u usage) Error() string { return u.err.Error() }
func (u usage) Unwrap() error { return u.err }

// LegacyArgs rewrites the single-dash long spellings this repository has always
// taken -- `-public`, `-since`, `-settings`, `-dry-run` -- into the double-dash
// form pflag understands, and leaves everything else byte-identical.
//
//	func main() { osExit(Run(RootCmd(), os.Args[1:])) }
//
// # Why a cobra command needs this at all
//
// ⛔ A SINGLE DASH IS NOT A LONG FLAG TO pflag, IT IS A CLUSTER OF SHORTHANDS.
// pflag@v1.0.9 flag.go:1038 parseSingleShortArg takes `-host` apart as the
// shorthands h, o, s, t and fails on the first one nothing declares. Under
// package flag, which every command here used until now, `-host` and `--host`
// were the same flag. So the migration to cobra silently breaks every
// invocation ever written down -- the ones in this README, the ones in the
// user's own notes, the ones in CI -- and `go test` stays green throughout,
// because tests drive RootCmd with arguments they spell themselves.
//
// ⛔ AND `-help` IS THE SHARPEST CASE, because it undoes what [ParseFlags]
// was written for. cobra command.go:919 hands a parse error to FlagErrorFunc
// BEFORE it looks at the help flag at :926, so `-help` -- which pflag reads as
// `-h` followed by an undeclared `e` -- exits non-zero even though the help
// flag was set on the way past. `-h`, `--help` and the `help` verb all exit 0
// by construction (command.go:1148 turns [flag.ErrHelp] into print-help and a
// nil error); `-help` alone would come back a usage error.
//
// # Why it enumerates instead of listing
//
// ⛔ THE FLEET'S OTHER ANSWER TO THIS IS A HAND-WRITTEN MAP. go-fleettools's
// gopin (PR #142, merged) carries `longFlags` -- seventeen names typed out by
// hand -- and the eighteenth flag somebody adds will have a single-dash
// spelling that fails, with nothing to say so. This asks the command instead:
// [pflag.FlagSet.VisitAll] over the very flag set that is about to parse the
// arguments. A name cannot fall out of step with itself.
//
// That is also why [cobra.Command.InitDefaultHelpFlag] is called here: cobra
// declares -h/--help at execute() time, so a VisitAll before that would not
// see `help`, and `-help` would be the one spelling this missed.
func LegacyArgs(root *cobra.Command, args []string) []string {
	if root == nil {
		return args
	}

	// Which command the flags belong to: `ghpkg delete -yes` has to be judged
	// against delete's flag set, not the root's. A Find that fails leaves the
	// root in place -- a bad verb is cobra's error to report, not ours.
	target := root
	if c, _, err := root.Find(args); err == nil && c != nil {
		target = c
	}
	target.InitDefaultHelpFlag()

	long := map[string]*pflag.Flag{}
	short := map[byte]*pflag.Flag{}
	add := func(f *pflag.Flag) {
		long[f.Name] = f
		if f.Shorthand != "" {
			short[f.Shorthand[0]] = f
		}
	}
	target.Flags().VisitAll(add)
	target.PersistentFlags().VisitAll(add)
	target.InheritedFlags().VisitAll(add)

	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]

		// Everything after a bare `--` is an operand by definition, including
		// arguments that look exactly like flags.
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}

		rewritten, f, hasValue := classify(a, long, short)
		out = append(out, rewritten)

		// ⛔ A FLAG'S VALUE IS NOT A FLAG, however much it looks like one.
		// `reauthor -to -identities` means the literal string `-identities`,
		// and rewriting it to `--identities` would not break the parse -- pflag
		// takes the next argument as the value whatever it starts with -- it
		// would silently change the value by one character.
		if f != nil && !hasValue && f.NoOptDefVal == "" && i+1 < len(args) {
			i++
			out = append(out, args[i])
		}
	}
	return out
}

// classify says what one argument is: the spelling to pass on, the flag it
// names if it names one, and whether it already carries its own value.
func classify(a string, long map[string]*pflag.Flag, short map[byte]*pflag.Flag) (string, *pflag.Flag, bool) {
	switch {
	case strings.HasPrefix(a, "--"):
		name, _, eq := strings.Cut(a[2:], "=")
		return a, long[name], eq

	case strings.HasPrefix(a, "-") && len(a) > 1:
		body, _, eq := strings.Cut(a[1:], "=")
		if len(body) > 1 {
			if f, ok := long[body]; ok {
				// The whole point: one more dash, nothing else touched.
				return "-" + a, f, eq
			}
			// A genuine cluster, `-rf`. Only its LAST shorthand can take a
			// value, the way pflag reads it.
			return a, short[body[len(body)-1]], eq
		}
		return a, short[body[0]], eq
	}

	// A bare `-` is stdin by convention, and an operand to pflag either way.
	return a, nil, false
}
