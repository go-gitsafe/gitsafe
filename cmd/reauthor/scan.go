package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ⛔ A scan is the half of this doctrine that runs on a schedule, and it exists
// for the same reason credscan does: the rewrite fixes one repository, and
// nothing says which ones need it. A sweep of 660 repositories in August 2026
// found 31 carrying a personal identity, and the only way that was discovered
// was by looking.
//
// It is READ-ONLY and exits non-zero when it finds something, so cron or a
// launchd job can gate on it.

// finding is one repository's worth of answer.
type finding struct {
	Dir    string
	Counts map[string]int // identity → commits, only the ones that are yours
	Err    error
}

func (f finding) total() int {
	n := 0
	for _, c := range f.Counts {
		n += c
	}
	return n
}

// scan walks roots for checkouts and says which ones still carry an identity of
// yours.
func scan(roots []string, own owned, stdout, stderr io.Writer) int {
	var found []finding
	walked, repos, unreadable, refused := 0, 0, 0, 0

	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				// A directory that refuses to be listed is NOT a clean one.
				refused++
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			walked++
			if _, err := os.Stat(filepath.Join(p, ".git")); err != nil {
				return nil
			}
			repos++
			f := look(p, own)
			if f.Err != nil {
				unreadable++
			}
			if f.Err != nil || len(f.Counts) > 0 {
				found = append(found, f)
			}
			return nil
		})
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].total() != found[j].total() {
			return found[i].total() > found[j].total()
		}
		return found[i].Dir < found[j].Dir
	})

	carrying, commits := 0, 0
	for _, f := range found {
		if f.Err != nil {
			fmt.Fprintf(stderr, "?    %s: %v\n", f.Dir, f.Err)
			continue
		}
		carrying++
		commits += f.total()
		fmt.Fprintf(stdout, "\n%s\n", f.Dir)
		for _, line := range byCount(f.Counts) {
			fmt.Fprintln(stdout, "   ", line)
		}
	}

	fmt.Fprintf(stdout, "\nwalked %d directories, %d checkouts\n", walked, repos)
	fmt.Fprintf(stdout, "%d carry an identity of yours, in %d commit(s)\n", carrying, commits)

	// ⛔ Completeness is reported separately from the answer, because a scan that
	// could not read must not be reported as a clean machine.
	if unreadable > 0 || refused > 0 {
		fmt.Fprintf(stderr, "\nreauthor: this scan did not cover everything: "+
			"%d checkout(s) would not answer, %d director(ies) refused listing.\n", unreadable, refused)
		fmt.Fprintln(stderr, "  A scan that could not read is not a clean machine.")
		return 1
	}
	if carrying > 0 {
		fmt.Fprintf(stdout, "\nTo fix one:  reauthor -identities <file> <dir>\n")
		fmt.Fprintln(stdout, "Nothing is pushed by that, and nothing was changed by this.")
		return 1
	}
	fmt.Fprintln(stdout, "nothing of yours is published from these checkouts")
	return 0
}

// look counts, per identity of yours, the commits reachable from any ref.
//
// It asks about BOTH the author and the committer: a commit someone else wrote
// and you rebased carries your address as committer, and that is published just
// as visibly.
func look(dir string, own owned) finding {
	f := finding{Dir: dir, Counts: map[string]int{}}
	g := &git{dir: dir}
	lines, err := g.lines("log", "--all", "--format=%an <%ae>%x09%cn <%ce>")
	if err != nil {
		// An empty repository has no commits and no error worth reporting; git
		// says so on stderr and exits non-zero, which is not a failure to read.
		if strings.Contains(err.Error(), "does not have any commits") ||
			strings.Contains(err.Error(), "unknown revision") {
			return f
		}
		f.Err = err
		return f
	}
	for _, l := range lines {
		for _, who := range strings.Split(l, "\t") {
			i, err := parseWho(who)
			if err != nil || !own.has(i) {
				continue
			}
			f.Counts[who]++
		}
	}
	return f
}
