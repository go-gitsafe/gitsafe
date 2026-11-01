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
// ⛔ THE FOUR-SPELLING CHECK LIVES IN [helparg], NOT HERE, AND THE REASON IS
// THE DEPENDENCY. It was here, and `gitpush` imported this package for those
// six lines alone -- linking cobra and pflag, which it never calls, into the
// one command that exists so a token never reaches a command line:
//
//	gitpush v0.21.1 (before cobra)    3 462 018 bytes
//	gitpush v0.27.0                   3 835 570 bytes   +373 552, +10.8%
//
// A package's import list is part of its interface. This one imports a flag
// parser on purpose; a command that forwards its arguments to `git push`
// cannot afford to.
//
// ⛔ AND THE NOTE THAT SAID "IsHelpRequest stays for the five commands that
// are deliberately not cobra" WAS WRONG. Only ONE of those five ever called
// it -- `gitpush`. The hooks, the credential helper and `guard-bash` are
// invoked by git or by the harness and answer no question at all. Measured by
// reading every import of this package, 2026-11-01.
//
// ⛔ THE TWIN IN go-fleettools/fleettools IS A COPY, DELIBERATELY. The same
// defect was measured there the same day (13 of 25 commands) and fixed as
// internal/fleet.ParseFlags. Twenty-five lines with no dependencies do not earn
// a shared module between two repositories; a THIRD copy would, and whoever
// writes it should make one instead of this comment.
package cliflags
