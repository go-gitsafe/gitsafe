// Command credscan finds and removes credentials embedded in git remote URLs.
//
//	credscan scan  [root...]                 report; non-zero if anything was found
//	credscan clean <root...> [--write]       strip them; a dry run without --write
//
// # Why this exists
//
// A GitHub classic personal access token sat in the fetch AND push URL of 117
// checkouts on one machine for three months, and nothing on the machine
// noticed. Git prints a remote URL on any fetch, so the token had to be treated
// as disclosed from the first day; it was revoked and reissued.
//
// Two detectors were written by hand during that incident and both were wrong,
// in opposite directions: one missed the five checkouts whose URL had no
// username in front of the token, the other called 55 ordinary
// `ssh://git@github.com/…` remotes leaks. Telling a secret from a username is
// therefore the whole job, and it lives in [credurl] where it can be tested
// against both halves of a table.
//
// # What it prints, and what it will not
//
// It reports the repository, the host, the configuration key, and the
// credential's PROPERTIES — issuer prefix, length, a short digest so two
// findings can be told apart. Never the credential. A tool that printed the
// secret it found would be the leak it is reporting.
//
// # Counting
//
// Every run says how many directories it walked, how many checkouts it found,
// how many it could not read, and how long it took. A scan that could not read
// exits 3 rather than reporting a clean machine: a sweep here once printed
// "4 files" for 4413 and read as a success.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/go-gitsafe/gitsafe/credfix"
)

// Exit codes. They are the interface a cron job or a hook uses, so they are
// named rather than spelled out at each return.
const (
	exitClean        = 0 // walked something, read all of it, found nothing
	exitFound        = 1 // at least one credential is in a URL
	exitUsage        = 2
	exitInconclusive = 3 // the scan established nothing, which is not "clean"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  credscan scan  [root...]              report credentials embedded in git remote URLs
  credscan clean <root...> [--write]    strip them, leaving the URL git should have had

  With no root, scan walks your home directory. clean insists on being told
  where to write.

flags:
  --write      actually rewrite (clean only); without it, clean prints what it would do
  --verbose    also list the checkouts that are clean

exit status:
  0  nothing found      1  a credential was found
  2  usage              3  the scan established nothing (nothing walked, or a
                           configuration it could not read) — which is not "clean"
`)
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "-h", "--help", "help":
		usage(stdout)
		return exitClean
	case "scan", "clean":
	default:
		fmt.Fprintf(stderr, "credscan: no such command %q\n", verb)
		usage(stderr)
		return exitUsage
	}

	fset := flag.NewFlagSet("credscan "+verb, flag.ContinueOnError)
	fset.SetOutput(stderr)
	write := fset.Bool("write", false, "actually rewrite")
	verbose := fset.Bool("verbose", false, "list clean checkouts too")
	// Flags may come after the roots: `credscan clean ~/src --write` is what a
	// person types, and Go's flag package stops at the first positional. A flag
	// silently ignored on a command that writes is a defect, not a convention.
	var roots, flags []string
	for i, a := range rest {
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, rest[i:]...)
			break
		}
		roots = append(roots, a)
	}
	if err := fset.Parse(flags); err != nil {
		return exitUsage
	}
	roots = append(roots, fset.Args()...)

	if verb == "scan" && *write {
		fmt.Fprintln(stderr, "credscan: scan never writes; use `credscan clean --write`")
		return exitUsage
	}
	if verb == "clean" && *write && len(roots) == 0 {
		// Defaulting to the home directory is right for a scan and wrong for a
		// rewrite: the machine this was written for has 2155 checkouts under
		// $HOME, and a person asking to repair one of them did not ask for the
		// other 2154.
		fmt.Fprintln(stderr, "credscan: clean --write needs the roots spelled out; it will not rewrite $HOME by default")
		return exitUsage
	}
	return sweep(verb, credfix.Roots(roots), *write, *verbose, stdout, stderr)
}

func sweep(verb string, roots []string, write, verbose bool, stdout, stderr io.Writer) int {
	start := time.Now()
	fmt.Fprintf(stdout, "credscan %s: %v\n", verb, roots)
	if verb == "clean" && !write {
		fmt.Fprintln(stdout, "  (dry run — nothing is written without --write)")
	}

	inspect := credfix.Inspect
	if verb == "clean" {
		inspect = func(dir string) credfix.Report { return credfix.Repair(dir, write) }
	}

	var dirty, repaired, remaining int
	var unreadable []credfix.Report
	st := credfix.Walk(roots, inspect, func(r credfix.Report) {
		if !r.Readable() {
			unreadable = append(unreadable, r)
			return
		}
		if len(r.Leaks) == 0 {
			if verbose {
				fmt.Fprintf(stdout, "ok   %s\n", r.Dir)
			}
			return
		}
		dirty++
		fmt.Fprintf(stdout, "\n%s\n", r.Dir)
		for _, e := range sorted(r.Leaks) {
			switch {
			case e.Repaired:
				repaired++
				fmt.Fprintf(stdout, "     %s: stripped and read back — now %s\n", e.Key, e.Clean)
			case !e.Rewritable():
				remaining++
				fmt.Fprintf(stdout, "     %s: %s\n", e.Key, e.Finding)
				fmt.Fprintf(stdout, "         not rewritten — this one needs a person: %s\n", why(e))
			case write:
				remaining++
				fmt.Fprintf(stdout, "     %s: %s\n", e.Key, e.Finding)
				fmt.Fprintf(stdout, "         REWRITE FAILED — the value did not read back clean\n")
			default:
				remaining++
				fmt.Fprintf(stdout, "     %s: %s\n", e.Key, e.Finding)
				fmt.Fprintf(stdout, "         would become %s\n", e.Clean)
			}
		}
		fmt.Fprintf(stdout, "     credentialed URLs: %d before, %d after\n", r.Before(), r.After())
	})

	for _, r := range unreadable {
		fmt.Fprintf(stderr, "\n?    %s: %v\n", r.Dir, r.Err)
	}

	// The counts come before the verdict, always, and whether anything was
	// found or not.
	fmt.Fprintf(stdout, "\nwalked %d directories in %s\n", st.Dirs, time.Since(start).Round(time.Millisecond))
	fmt.Fprintf(stdout, "found %d checkouts, %d distinct configurations, %d unreadable, %d directories refused listing\n",
		st.Repos, st.Unique, st.Unreadable, st.DenyErrors)
	fmt.Fprintf(stdout, "%d of them carry a credential: %d URL(s) still do", dirty, remaining)
	if repaired > 0 {
		fmt.Fprintf(stdout, ", %d stripped and verified", repaired)
	}
	fmt.Fprintln(stdout)

	if !st.Complete() {
		fmt.Fprintf(stderr, "\ncredscan: this run established nothing.%s\n", incomplete(st))
		fmt.Fprintln(stderr, "  A scan that could not read must not be reported as a clean machine.")
		return exitInconclusive
	}
	if remaining > 0 {
		if verb == "scan" || !write {
			fmt.Fprintf(stdout, "\nThese must be treated as DISCLOSED: git prints a remote URL on any fetch.\n")
			fmt.Fprintf(stdout, "Revoke and reissue the credential, then:  credscan clean <root> --write\n")
		}
		return exitFound
	}
	return exitClean
}

func incomplete(st credfix.Stats) string {
	switch {
	case st.Repos == 0:
		return " It found no git checkout at all — check the roots."
	default:
		return fmt.Sprintf(" %d checkout(s) would not answer; on this machine a git that cannot"+
			" run still exits 0, so silence is not a clean result.", st.Unreadable)
	}
}

func why(e credfix.Entry) string {
	switch {
	case e.Finding.Scp:
		return "an scp-style URL cannot be repaired by dropping its user — `host:path` is ambiguous"
	case e.InKey:
		return "the credential is in the configuration KEY — a url.<base>.insteadOf rewrite rule; where it should point instead is a decision, not a repair"
	case e.Clean == "":
		return "there is no unambiguous clean form of this value, or the key holds more than one"
	default:
		return "only a remote's own fetch or push URL is rewritten automatically"
	}
}

func sorted(es []credfix.Entry) []credfix.Entry {
	out := make([]credfix.Entry, len(es))
	copy(out, es)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
