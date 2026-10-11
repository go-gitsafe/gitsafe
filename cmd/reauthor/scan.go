package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-gitsafe/gitsafe/credurl"
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
	Host   string         // where origin points, or "" for a repository with no remote
	Err    error
}

// where names the group this repository belongs to.
//
// ⛔ It is the REMOTE's host and not the path, because the path does not say
// whether anything is published. On the machine this was written for,
// `localhost/tools` has no remote at all while a directory under `github.com/`
// may be a worktree of something else — and the first sweep of this was read as
// "350 repositories carry an identity" when 215 of them were on an institution's
// own GitLab, where an institutional address belongs, and 71 had no remote.
func (f finding) where() string {
	if f.Host == "" {
		return "(no remote — published nowhere)"
	}
	return f.Host
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
			f.Host = originHost(p)
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
	perHost := map[string]int{}
	reposPerHost := map[string]int{}
	for _, f := range found {
		if f.Err != nil {
			fmt.Fprintf(stderr, "?    %s: %v\n", f.Dir, f.Err)
			continue
		}
		carrying++
		commits += f.total()
		perHost[f.where()] += f.total()
		reposPerHost[f.where()]++
		// ⛔ The host goes on the repository's own line too. A summary that
		// counts 88 repositories on a public host without naming them is not
		// actionable, and the reader then groups the output by PATH to find
		// them — which is the mistake this grouping exists to prevent.
		fmt.Fprintf(stdout, "\n%s\n    %s\n", f.Dir, f.where())
		for _, line := range byCount(f.Counts) {
			fmt.Fprintln(stdout, "   ", line)
		}
	}

	fmt.Fprintf(stdout, "\nwalked %d directories, %d checkouts\n", walked, repos)
	fmt.Fprintf(stdout, "%d carry an identity of yours, in %d commit(s)\n", carrying, commits)

	// ⛔ And WHERE, because the total on its own is the wrong number to act on.
	// Grouping is in here rather than left to whoever reads the output: the first
	// sweep's "350 repositories" was only actionable once broken down, and doing
	// that by hand afterwards is how a tool's answer gets restated wrongly.
	if len(perHost) > 0 {
		fmt.Fprintln(stdout, "\nby where origin points — an institutional address on that")
		fmt.Fprintln(stdout, "institution's own forge is not the same finding as one on a public host:")
		type row struct {
			host  string
			repos int
			n     int
		}
		var rows []row
		for h, n := range perHost {
			rows = append(rows, row{h, reposPerHost[h], n})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].n != rows[j].n {
				return rows[i].n > rows[j].n
			}
			return rows[i].host < rows[j].host
		})
		for _, r := range rows {
			fmt.Fprintf(stdout, "    %5d commit(s) in %4d repo(s)   %s\n", r.n, r.repos, r.host)
		}
	}

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

// originHost is the host the origin remote points at, or "" when there is none.
//
// ⭐ It reuses credurl.Inspect rather than parsing a URL again: that parser
// already knows the scp form `git@host:path`, which has no scheme, and two
// parsers of the same thing is how one of them ends up wrong.
func originHost(dir string) string {
	g := &git{dir: dir}
	raw, err := g.out("config", "remote.origin.url")
	if err != nil || raw == "" {
		return ""
	}
	if h := credurl.Inspect(raw).Host; h != "" {
		return h
	}
	// A local path as a remote is a remote, and it is not published anywhere a
	// host would name. Saying so beats calling it "no remote".
	return "(a local path)"
}
