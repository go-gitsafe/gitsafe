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
	Repo   string         // the git-common-dir: the same for a worktree and its repository
	Origin string         // a key for the UPSTREAM this points at, "" when there is none
	Main   bool           // true when this checkout is the repository itself, not a worktree of it
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
// upstream is what a person acts on: the repository this points at, or the
// reason there is none.
func (f finding) upstream() string {
	if f.Origin == "" {
		return f.where()
	}
	return f.Origin
}

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
			f.Repo, f.Main = repoOf(p)
			f.Origin = originKey(p)
			if f.Err != nil {
				unreadable++
			}
			if f.Err != nil || len(f.Counts) > 0 {
				found = append(found, f)
			}
			return nil
		})
	}

	// ⛔ One entry per REPOSITORY, not per checkout. A worktree shares its
	// repository's history, so counting checkouts counted the same commits once
	// per worktree: the first grouped sweep reported 89 GitHub-hosted carriers
	// and 13323 commits, of which ~80 were agent worktrees of ONE fork. A
	// worktree is not a separate publication.
	//
	// The repository's own checkout is preferred for display when it was walked,
	// because that is the directory a person acts on.
	best := map[string]finding{}
	worktrees := 0
	for _, f := range found {
		if f.Err != nil {
			continue
		}
		key := f.Repo
		if key == "" {
			key = f.Dir
		}
		prev, seen := best[key]
		if !seen {
			best[key] = f
			continue
		}
		worktrees++
		if f.Main && !prev.Main {
			best[key] = f
		}
	}
	var collapsed []finding
	for _, f := range found {
		if f.Err != nil {
			collapsed = append(collapsed, f)
		}
	}
	for _, f := range best {
		collapsed = append(collapsed, f)
	}
	found = collapsed

	sort.Slice(found, func(i, j int) bool {
		if found[i].total() != found[j].total() {
			return found[i].total() > found[j].total()
		}
		return found[i].Dir < found[j].Dir
	})

	carrying, commits, clones := 0, 0, 0
	perHost := map[string]int{}
	reposPerHost := map[string]int{}
	seenOrigin := map[string]bool{}
	origins := map[string]bool{}
	for _, f := range found {
		if f.Err != nil {
			fmt.Fprintf(stderr, "?    %s: %v\n", f.Dir, f.Err)
			continue
		}
		carrying++
		commits += f.total()
		perHost[f.where()] += f.total()
		if key := f.Origin; key == "" {
			reposPerHost[f.where()]++
		} else if !seenOrigin[f.where()+" "+key] {
			seenOrigin[f.where()+" "+key] = true
			reposPerHost[f.where()]++
		} else {
			clones++
		}
		if f.Origin != "" {
			origins[f.Origin] = true
		}
		// ⛔ The host goes on the repository's own line too. A summary that
		// counts 88 repositories on a public host without naming them is not
		// actionable, and the reader then groups the output by PATH to find
		// them — which is the mistake this grouping exists to prevent.
		// ⛔ The UPSTREAM on the line, not just its host. The summary counts
		// upstreams; naming only the host leaves the reader to work out which
		// ones, which is the same "counts without naming" gap one level up.
		fmt.Fprintf(stdout, "\n%s\n    %s\n", f.Dir, f.upstream())
		for _, line := range byCount(f.Counts) {
			fmt.Fprintln(stdout, "   ", line)
		}
	}

	fmt.Fprintf(stdout, "\nwalked %d directories, %d checkouts\n", walked, repos)
	if worktrees > 0 {
		fmt.Fprintf(stdout, "%d of the carriers were worktrees of a repository already counted, "+
			"and are folded into it: a worktree shares its repository's history, so counting it "+
			"separately counts the same commits twice\n", worktrees)
	}
	fmt.Fprintf(stdout, "%d checkout(s) carry an identity of yours, in %d commit(s)\n",
		carrying, commits)
	// ⛔ And the number to ACT on is neither of those. Several clones of one
	// upstream are one repository to fix, and fixing it once fixes them all:
	// three clones of the same project were counted as three here, and twelve
	// sandbox clones of one fork as twelve.
	//
	// Every checkout is still listed above, because a clone can hold a commit
	// that was never pushed, and folding the LISTING would hide it.
	if len(origins) > 0 {
		fmt.Fprintf(stdout, "%d distinct upstream(s) to fix", len(origins))
		if clones > 0 {
			fmt.Fprintf(stdout, "; %d of the carriers are further clones of one already counted", clones)
		}
		fmt.Fprintln(stdout)
	}

	// ⛔ And WHERE, because the total on its own is the wrong number to act on.
	// Grouping is in here rather than left to whoever reads the output: the first
	// sweep's "350 repositories" was only actionable once broken down, and doing
	// that by hand afterwards is how a tool's answer gets restated wrongly.
	if len(perHost) > 0 {
		fmt.Fprintln(stdout, "\nby UPSTREAM, grouped by host — an institutional address on that")
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

// repoOf is the repository a checkout belongs to, and whether this checkout IS
// that repository rather than a worktree of it.
//
// ⛔ git-common-dir is the same for a worktree and its repository, which is what
// makes a worktree foldable. Without this, a repository with eighty agent
// worktrees counts its history eighty-one times — and it was eighty-one agent
// worktrees of one fork that turned 2 carriers into 89.
func repoOf(dir string) (string, bool) {
	g := &git{dir: dir}
	common, err := g.out("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		// Older git has no --path-format; the relative answer resolves against
		// the checkout, which is enough to tell a worktree from its repository.
		if common, err = g.out("rev-parse", "--git-common-dir"); err != nil {
			return "", true
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(dir, common)
		}
	}
	own, err := g.out("rev-parse", "--absolute-git-dir")
	if err != nil {
		return common, true
	}
	return common, own == common
}

func plural(n int) string {
	if n == 1 {
		return "Y"
	}
	return "IES"
}

// originKey identifies the UPSTREAM a checkout points at, so that several clones
// of one project count once.
//
// ⛔ It is a KEY and not a parsed URL: host plus path, lowercased, with a
// trailing ".git" and any leading slashes removed, so that
// `git@github.com:o/r.git` and `https://github.com/o/r` answer the same. The host
// comes from credurl, which already knows the scp form; only the path is taken
// apart here, and only far enough to compare two spellings of one repository.
func originKey(dir string) string {
	g := &git{dir: dir}
	raw, err := g.out("config", "remote.origin.url")
	if err != nil || raw == "" {
		return ""
	}
	host := credurl.Inspect(raw).Host
	path := raw
	if i := strings.Index(path, "://"); i >= 0 {
		path = path[i+3:]
		if j := strings.Index(path, "@"); j >= 0 {
			path = path[j+1:]
		}
		if j := strings.Index(path, "/"); j >= 0 {
			path = path[j:]
		} else {
			path = ""
		}
	} else if i := strings.Index(path, ":"); i >= 0 && host != "" {
		path = path[i+1:]
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" {
		// A local path as a remote: the path itself identifies it.
		return strings.ToLower(strings.TrimSuffix(raw, ".git"))
	}
	return strings.ToLower(host + "/" + path)
}
