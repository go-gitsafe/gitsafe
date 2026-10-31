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
// ⛔ MEASURED, 2026-10-18: of the 16 commands in this repository, 10 answered
// `-h` with exit 2. `ghrelease -h`, typed to read its flags, printed its usage
// and exited 2 -- which is what started this. These are the tools a person
// reaches for while holding a credential they must not print; the moment to
// find out what `ghpkg delete` takes is BEFORE running it, and a tool that
// treats the asking as an error is one people stop asking.
//
// # What answers it now
//
// Every command that parses flags is a cobra command driven through [Run]
// (legacy.go), which keeps the three statuses apart -- 0 for work or a
// question in any of four spellings, 1 for a failure, 2 for a misuse -- and
// through [LegacyArgs], which keeps the single-dash spellings working. The
// helpers this package used to carry for the flag package, ParseFlags and
// IsHelpError, were removed when the last command that called them moved.
//
// [IsHelpRequest] stays for the five commands that are deliberately not cobra
// (gitpush, guard-bash, git-credential-tokenfile, git-pre-push-guard,
// git-post-checkout-guard): a parser is the wrong shape for all of them.
//
// ⛔ THE TWIN IN go-fleettools/fleettools IS A COPY, DELIBERATELY. The same
// defect was measured there the same day (13 of 25 commands) and fixed as
// internal/fleet.ParseFlags. Twenty-five lines with no dependencies do not earn
// a shared module between two repositories; a THIRD copy would, and whoever
// writes it should make one instead of this comment.
package cliflags

// IsHelpRequest reports whether this argument is somebody asking what the
// command does, in all four spellings people type.
//
// ⛔ IT IS FOR THE COMMANDS THAT HAVE NO FLAG SET AT ALL, where `-h` is not a
// verb and no parser will see it. No two of them knew the same spellings:
// credscan knew three and missed `-help` (so `credscan -help` printed the usage
// and exited 2 while `credscan -h` printed the same usage and exited 0), gitpush
// knew two, ghpkg and ghscopes knew none. ghscopes is the sharpest: its parse
// loop appended an unrecognised argument to the list of DEMANDED SCOPES, so
// `ghscopes -h` read the token, asked GitHub about it, and reported that a
// scope called `-h` was missing.
func IsHelpRequest(arg string) bool {
	switch arg {
	case "-h", "-help", "--help", "help":
		return true
	}
	return false
}
