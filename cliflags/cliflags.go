// Package cliflags answers one question for every command in this repository:
// was this a QUESTION or a MISTAKE?
//
// # Why it exists
//
// The flag package reports a help request as an error like any other,
// [flag.ErrHelp], and the two-line shape every command here was written with
// cannot tell it from a typo:
//
//	if err := fs.Parse(args); err != nil {
//		return 2
//	}
//
// flag.ExitOnError does tell them apart -- os.Exit(0) for ErrHelp, os.Exit(2)
// for the rest -- but every command here uses flag.ContinueOnError so that
// run() can be tested instead of exiting the process, and that choice silently
// gave the distinction up.
//
// ⛔ MEASURED, 2026-10-18: of the 16 commands in this repository, 10 answered
// `-h` with exit 2. `ghrelease -h`, typed to read its flags, printed its usage
// and exited 2 -- which is what started this. These are the tools a person
// reaches for while holding a credential they must not print; the moment to
// find out what `ghpkg delete` takes is BEFORE running it, and a tool that
// treats the asking as an error is one people stop asking.
//
// ⛔ THE TWIN IN go-fleettools/fleettools IS A COPY, DELIBERATELY. The same
// defect was measured there the same day (13 of 25 commands) and fixed as
// internal/fleet.ParseFlags. Twenty-five lines with no dependencies do not earn
// a shared module between two repositories; a THIRD copy would, and whoever
// writes it should make one instead of this comment.
package cliflags

import (
	"errors"
	"flag"
)

// ParseFlags parses args and says what the command should do about it: the
// status to exit with, and whether there is any work left to do.
//
//	if code, ok := cliflags.ParseFlags(fs, args); !ok {
//		return code
//	}
//
// It is one helper rather than ten copies because the help branch is the one
// nobody exercises: ten hand-written copies are ten branches to cover, and
// NONE of the ten commands here had covered it.
func ParseFlags(fs *flag.FlagSet, args []string) (code int, ok bool) {
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return 0, false
	case err != nil:
		return 2, false
	}
	return 0, true
}

// IsHelpError reports whether a flag set refused because it was ASKED
// something, rather than given something wrong.
//
// It is for the commands whose run() returns an error rather than a status --
// ghnew is the one here -- where "help" means `return nil` and the two-value
// shape of [ParseFlags] does not fit.
func IsHelpError(err error) bool { return errors.Is(err, flag.ErrHelp) }

// IsHelpRequest reports whether this argument is somebody asking what the
// command does, in all four spellings people type.
//
// ⛔ IT IS FOR THE COMMANDS [ParseFlags] CANNOT HELP: the ones with no flag set
// at all, or that dispatch on a subcommand before any flag set exists, where
// `-h` is not a verb. No two of them knew the same spellings: credscan knew
// three and missed `-help` (so `credscan -help` printed the usage and exited 2
// while `credscan -h` printed the same usage and exited 0), gitpush knew two,
// ghpkg and ghscopes knew none. ghscopes is the sharpest: its
// parse loop appends an unrecognised argument to the list of DEMANDED SCOPES,
// so `ghscopes -h` reads the token, asks GitHub about it, and reports that a
// scope called `-h` is missing.
func IsHelpRequest(arg string) bool {
	switch arg {
	case "-h", "-help", "--help", "help":
		return true
	}
	return false
}
