// Package helparg answers one question, in the four spellings people type:
// is this argument somebody asking what the command does?
//
// # Why it is its own package
//
// ⛔ MEASURED 2026-11-01, AND THE REASON IS THE DEPENDENCY, NOT THE CODE. This
// function used to live in [cliflags], beside the cobra adapter. cobra is the
// fleet's rule for a command-line tool and the eleven commands here that take
// flags are built on it — but `gitpush` is not one of them: it forwards its
// whole argument list to `git push`, so `--force-with-lease` and `-u` are
// operands it must not interpret. It imported `cliflags` for these six lines
// alone, and linked cobra and pflag along with them:
//
//	gitpush v0.21.1 (before cobra)    3 462 018 bytes
//	gitpush v0.27.0                   3 835 570 bytes   +373 552, +10.8%
//
// Three hundred and sixty-five kilobytes of a flag parser that is never
// called, in the one command that exists so a token never reaches a command
// line. `gitpush`'s own source says why that matters: `errorsAs` is written
// out there by hand rather than imported, "kept local so the import list of a
// security tool stays as short as it can be".
//
// ⭐ `guard-bash` was measured at the same time and is unchanged — 4 547 538
// to 4 547 666 bytes, 128 bytes of noise — which matters more than it looks:
// it runs on EVERY Bash call on a machine that has it wired in, so it is the
// hottest path in the repository and it imports none of this.
//
// This package has no imports at all. That is the point of it.
package helparg

// Is reports whether arg is somebody asking what the command does.
//
// ⛔ IT IS FOR THE COMMANDS A PARSER CANNOT HELP: the ones that forward their
// arguments to another program, or dispatch on a verb before any flag set
// exists, where `-h` is not a verb. The four spellings are not a style
// choice: before this was one list, no two commands knew the same ones.
// `credscan` knew three and missed `-help`, so `credscan -help` printed the
// usage and exited 2 while `credscan -h` printed the same usage and exited 0;
// `gitpush` knew two; `ghpkg` and `ghscopes` knew none. `ghscopes` was the
// sharpest, since its parse loop appended an unrecognised argument to the
// list of DEMANDED SCOPES — `ghscopes -h` read the token, asked GitHub about
// it, and reported that a scope called `-h` was missing.
//
// The negative half matters as much: `-help-me` is not a request for help,
// and neither is a repository called `help`.
func Is(arg string) bool {
	switch arg {
	case "-h", "-help", "--help", "help":
		return true
	}
	return false
}
