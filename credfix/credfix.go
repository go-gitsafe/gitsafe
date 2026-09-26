// Package credfix finds and repairs credentials embedded in the remote URLs a
// git repository has written down.
//
// [credurl] answers the question about one URL. This package is the part that
// touches a machine: it walks for checkouts, asks git what each one has in its
// local configuration, and rewrites the URLs that carry a secret so that git
// falls back to a credential helper.
//
// # Two rules it is built around
//
// A repair verifies itself. Every rewrite is read back out of git before it is
// counted, because a repair that reports success without looking is how the
// incident behind this package lasted three months.
//
// A scan that could not read must not report zero. Every count is reported —
// directories walked, checkouts found, configurations that could not be read —
// and a repository whose configuration came back empty is UNREADABLE, never
// clean. A repository always has core.repositoryformatversion, so nothing is a
// legitimate zero; and on this machine `git` can be a stub that prints a licence
// refusal and still exits 0, which is exactly how a guard comes to report a
// clean bill of health it never established.
package credfix

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/go-gitsafe/gitsafe/credurl"
)

// Entry is one configuration key whose value — or whose own name — carries a
// credential.
type Entry struct {
	// Key is the configuration key, e.g. `remote.origin.url`. A credential can
	// be part of a key rather than a value (`url.https://TOKEN@host/.insteadOf`
	// is a legal rewrite rule), so this is stored with any credential stripped
	// out of it and is safe to print.
	Key string
	// Finding describes the credential without holding it.
	Finding credurl.Finding
	// Clean is the URL the value should be: the same URL with no userinfo. It
	// is empty when this package will not rewrite the value — see
	// [credurl.Strip] on scp-style URLs, and the keys below that are not remote
	// URLs.
	Clean string
	// InKey is true when the credential is part of the key rather than of the
	// value — `url.https://TOKEN@host/.insteadOf` is a legal rewrite rule, and
	// nothing here rewrites one: where that URL should point is a decision.
	InKey bool
	// Repaired is set once the rewrite has been read back and confirmed.
	Repaired bool
}

// Rewritable reports whether [Repair] will touch this entry.
//
// Any single-valued key whose VALUE is a credentialed URL is rewritten, whatever
// the key is called, because the clean form of such a URL is the same everywhere
// it appears: the URL with no userinfo. This started out restricted to
// `remote.<name>.url` and `.pushurl`, and a scan of this machine found what that
// missed — three checkouts still carrying a token in `branch.<name>.remote`,
// which git allows to be a URL, months after every remote URL had been
// cleaned. A repair aimed at the key a person happens to think of is a repair
// that leaves the others.
//
// What is NOT rewritten is a credential in the KEY — `url.<base>.insteadOf` is a
// rewrite rule and where it should point instead is a decision — and a key
// holding more than one value, which [Repair] discovers and reports. Both are
// named rather than guessed at.
func (e Entry) Rewritable() bool { return e.Clean != "" && !e.InKey }

// Report is what one repository turned out to hold.
type Report struct {
	// Dir is the working tree the report is about.
	Dir string
	// Config is the file git read its local configuration from. Two checkouts
	// that share one file — a repository and its linked worktrees — report the
	// same path, which is how they are counted once.
	Config string
	// Keys is how many configuration keys were read. Zero means the read
	// failed, whatever git's exit status said.
	Keys int
	// Leaks are the entries carrying a credential.
	Leaks []Entry
	// Err is why this repository could not be read.
	Err error
}

// Readable reports whether git answered at all.
func (r Report) Readable() bool { return r.Err == nil && r.Keys > 0 }

// Before is how many credentialed URLs the repository held.
func (r Report) Before() int { return len(r.Leaks) }

// After is how many it still holds.
func (r Report) After() int {
	n := 0
	for _, e := range r.Leaks {
		if !e.Repaired {
			n++
		}
	}
	return n
}

// git runs git in dir and returns its standard output.
//
// It is a variable so that a test can make git fail, or answer as the stub that
// prints a licence refusal and exits 0 — the failure this package must not read
// as "clean".
//
// Its second result is what git said on stderr, EVEN WHEN IT SUCCEEDED. That
// is not tidiness: the git on this machine can print `xcrun: error: invalid
// active developer path` and exit 0, and a caller that only looks at the exit
// status reads that as an empty answer from a clean repository.
var git = func(dir string, args ...string) ([]byte, string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	said := strings.TrimSpace(errb.String())
	if err != nil {
		return out.Bytes(), said, fmt.Errorf("git %s: %v: %s", args[0], err, said)
	}
	return out.Bytes(), said, nil
}

// Inspect reads one repository's local configuration and reports what carries a
// credential. It writes nothing.
func Inspect(dir string) Report {
	r := Report{Dir: dir}
	out, said, err := git(dir, "config", "--local", "--list", "--show-origin", "-z")
	if err != nil {
		r.Err = err
		return r
	}
	// git writes two NUL-terminated fields per entry: `file:<path>` then
	// `<key>\n<value>`. A value may contain a newline and may not contain a
	// NUL, so the pairs are what can be relied on — the key/value split is
	// only the FIRST newline.
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		origin, kv := fields[i], fields[i+1]
		r.Keys++
		if r.Config == "" {
			// git prints this path RELATIVE to its working directory —
			// `file:.git/config` — so every repository on the machine has the
			// same "origin" until it is resolved. Measured, not assumed: the
			// first version of this deduplicated 2155 checkouts down to one.
			p := strings.TrimPrefix(origin, "file:")
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			// And the two spellings of one path are not equal as strings. From
			// a linked worktree git prints the shared config ABSOLUTE, from the
			// main checkout it prints it relative — and on macOS the absolute
			// one comes back through /private/tmp while the walk arrived via
			// /tmp. Symlinks are resolved so that a repository and its
			// worktrees are one configuration, which is what the count claims.
			if real, err := filepath.EvalSymlinks(p); err == nil {
				p = real
			}
			r.Config = p
		}
		key, value, _ := strings.Cut(kv, "\n")
		// The key is checked as well as the value: a credential can be part of
		// a key, and a key is what a report prints.
		// The value first, then the key: a map here would make the precedence
		// depend on Go's randomised iteration order, so a repository with a
		// credential in both would report a different one each run.
		for _, half := range []struct {
			inKey bool
			s     string
		}{{false, value}, {true, key}} {
			f := credurl.Inspect(half.s)
			if !f.Leak() {
				continue
			}
			clean, _ := credurl.Strip(value)
			if clean == value {
				clean = "" // nothing this can rewrite
			}
			safeKey, _ := credurl.Strip(key)
			r.Leaks = append(r.Leaks, Entry{Key: safeKey, Finding: f, Clean: clean, InKey: half.inKey})
			break
		}
	}
	if r.Keys == 0 {
		r.Err = fmt.Errorf("git read no configuration at all, which no repository has")
		if said != "" {
			// What git printed while exiting 0 is usually the whole
			// explanation, and it is lost by every caller that only checks the
			// exit status.
			r.Err = fmt.Errorf("%w — git said: %s", r.Err, said)
		}
	}
	return r
}

// Repair strips the credential from every remote URL it can, and verifies each
// rewrite by reading the value back out of git.
//
// With write false it changes nothing and reports what it would do, which is the
// default everywhere this is called from: the repair is irreversible in the
// sense that matters — it is the moment a person finds out their fetches now
// need a credential helper.
//
// The credential never reaches a command line. Only the CLEANED value is passed
// to git, which is why this uses --replace-all on a key rather than any of the
// forms that name the old value.
func Repair(dir string, write bool) Report {
	r := Inspect(dir)
	if !r.Readable() || !write {
		return r
	}
	for i := range r.Leaks {
		e := &r.Leaks[i]
		if !e.Rewritable() {
			continue
		}
		// One value per key is the case that exists; a multi-valued remote URL
		// would be collapsed by --replace-all, so it is left for a person
		// instead. Naming it is the honest outcome: this cannot tell which of
		// two URLs a person meant to keep.
		if n := countValues(dir, e.Key); n != 1 {
			e.Clean = ""
			continue
		}
		// A failed write is not reported without looking either. Two checkouts
		// that share a configuration — a repository and its linked worktree —
		// are repaired concurrently by [Walk], git locks the file, and one of
		// the two is refused for a value the other has already fixed. Reading
		// it back answers "is it clean now" for both, which is the question;
		// "did my write succeed" is not.
		_, _, _ = git(dir, "config", "--local", "--replace-all", e.Key, e.Clean)
		got, _, err := git(dir, "config", "--local", "--get", e.Key)
		if err != nil {
			continue
		}
		v := strings.TrimRight(string(got), "\n")
		if v == e.Clean && !credurl.Inspect(v).Leak() {
			e.Repaired = true
		}
	}
	return r
}

func countValues(dir, key string) int {
	out, _, err := git(dir, "config", "--local", "--get-all", key)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimRight(string(out), "\n"), "\n"))
}

// Stats is what a walk actually looked at. It is reported whether anything was
// found or not: a sweep here once printed "4 files" for 4413 because the
// command it used had no such flag, and a zero that nobody could distinguish
// from a clean result is the failure this is written against.
type Stats struct {
	// Dirs is how many directories were entered.
	Dirs int
	// Repos is how many checkouts were found.
	Repos int
	// Unique is how many distinct configurations those checkouts share; a
	// repository and its linked worktrees are one.
	Unique int
	// Unreadable is how many could not be read.
	Unreadable int
	// DenyErrors is how many directories could not be listed at all. A home
	// directory always has some, and they are not a reason to fail — but they
	// are a reason not to claim the walk was complete.
	DenyErrors int
	// Roots are the roots that were walked.
	Roots []string
}

// Complete reports whether the walk saw enough to be evidence of anything. A
// walk that found no repository, or could not read one it found, has not
// established that a machine is clean.
func (s Stats) Complete() bool { return s.Repos > 0 && s.Unreadable == 0 }

// Walk finds every git checkout under roots and calls fn with each one's
// report, once per distinct configuration.
//
// Reports are gathered concurrently — the walk is dominated by the per-
// repository git calls — and fn is called from one goroutine, in the order the
// reports arrive.
func Walk(roots []string, inspect func(dir string) Report, fn func(Report)) Stats {
	st := Stats{Roots: roots}
	var dirs []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				st.DenyErrors++
				return nil
			}
			if d.Name() != ".git" {
				if d.IsDir() {
					st.Dirs++
				}
				return nil
			}
			// A .git directory is a repository here; a .git FILE is a linked
			// worktree or a submodule, whose configuration is elsewhere and is
			// found by asking git rather than by guessing.
			st.Repos++
			dirs = append(dirs, filepath.Dir(p))
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		})
	}

	type result struct {
		r Report
	}
	const workers = 16
	in := make(chan string)
	outc := make(chan result)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range in {
				outc <- result{inspect(d)}
			}
		}()
	}
	go func() {
		for _, d := range dirs {
			in <- d
		}
		close(in)
		wg.Wait()
		close(outc)
	}()

	seen := map[string]bool{}
	for res := range outc {
		r := res.r
		if !r.Readable() {
			st.Unreadable++
			fn(r)
			continue
		}
		if seen[r.Config] {
			continue
		}
		seen[r.Config] = true
		st.Unique++
		fn(r)
	}
	return st
}

// Roots returns the roots to walk: the ones given, or the home directory.
func Roots(args []string) []string {
	if len(args) > 0 {
		out := make([]string, 0, len(args))
		for _, a := range args {
			if abs, err := filepath.Abs(a); err == nil {
				a = abs
			}
			out = append(out, a)
		}
		sort.Strings(out)
		return out
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return []string{"."}
	}
	return []string{home}
}
