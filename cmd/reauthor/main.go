// Command reauthor rewrites the commits that are YOURS to one identity and
// leaves everybody else's byte for byte, so that a personal name or address
// stops being published.
//
// # Why this exists
//
// A sweep of 660 repositories on one machine in August 2026 found four
// identifiers where only the public one should have been: a personal address
// under the right name in the root commit of most of them — reachable from every
// branch and every tag — and a run of commits made with `git -c user.name` in
// one session. 31 repositories carried one.
//
// # What it preserves
//
// Trees, messages, parents (merges included), author dates and committer dates,
// all byte for byte. It reads and writes raw commit objects rather than passing
// dates through a format, so a timestamp cannot be reinterpreted on the way
// through. The working tree comes out identical, and every Go module hash stays
// valid, because a module zip is built from the tree and not from the commit.
//
// # What it refuses
//
// It refuses a repository with uncommitted changes to TRACKED files; untracked
// ones are nobody's business and a build directory is no reason to stop.
//
// It refuses to run twice at once, with a lock directory in the git dir. Killing
// the shell that started a rewrite does not kill the rewrite: in August 2026 that
// left two of them running on the same repository, both heading for update-ref
// and a hard reset on the same refs. `mkdir` is the test and the claim in one
// step, so two instances cannot both win it.
//
// It refuses to finish quietly if any tree changed, or if any of your identities
// is still reachable from a branch or a tag afterwards.
//
// # What it will not do
//
// It does not push. Every hash changes, so publishing means force-pushing
// branches AND tags, which is an act somebody takes on purpose:
//
//	gitpush --force origin --all
//	gitpush --force origin --tags
//
// `--force`, not `--force-with-lease`: the lease on a rewritten tag is stale by
// construction and the push is rejected.
//
// ⛔ And do not fetch in between. `git fetch --tags` brings the old tags back
// down and silently undoes part of the rewrite — it cost two repositories their
// tags in August 2026, from a command run only to see what the remote held. Ask
// the remote with `git ls-remote`, which writes nothing.
//
// # Your identities are an argument, never a list in here
//
// The shell version this replaces carried seven personal addresses and a
// personal name in its source. Publishing that would publish exactly what the
// tool exists to remove, and a list in a program is wrong for a second reason:
// it names values rather than describing them, so it dies the day one changes.
// So the identities come from a file, and there is no default list:
//
//	# one per line, blank lines and # comments ignored
//	email: someone@example.invalid
//	name:  Some Name
//
// A name matches whatever the address is — a commit under your name with a
// stranger's address is still yours. An address matches whatever the name is,
// which is the case the first sweep missed: a name being right is not evidence
// that the address is.
//
// ⛔ Only your own identities belong in that file. A blanket search would be
// worse than useless here: on one repository a contributor's address was at the
// same institution as the owner's, and his single commit is precisely what this
// tool exists to preserve.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reauthor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	identities := fs.String("identities", "", "file listing the identities that are YOURS (required)")
	to := fs.String("to", "", `the identity to rewrite them to, as "Name <email>"; default: git's configured user.name and user.email`)
	dry := fs.Bool("dry-run", false, "say what would change, write nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *identities == "" {
		fmt.Fprintln(stderr, "reauthor: -identities is required: a list of identities in the source "+
			"would publish what this removes, and would name values instead of describing them")
		fs.Usage()
		return 2
	}
	repo := "."
	if rest := fs.Args(); len(rest) == 1 {
		repo = rest[0]
	} else if len(rest) > 1 {
		fmt.Fprintln(stderr, "reauthor: one repository at a time")
		return 2
	}

	g := &git{dir: repo}
	own, err := readIdentities(*identities)
	if err != nil {
		fmt.Fprintln(stderr, "reauthor:", err)
		return 1
	}
	want, err := target(g, *to)
	if err != nil {
		fmt.Fprintln(stderr, "reauthor:", err)
		return 1
	}

	code, err := rewrite(g, own, want, *dry, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "reauthor:", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// identity is a name and an address, as a commit header spells them.
type identity struct{ Name, Email string }

func (i identity) String() string { return i.Name + " <" + i.Email + ">" }

// owned is the set of identities that are yours, by address or by name.
type owned struct {
	emails map[string]bool
	names  map[string]bool
}

func (o owned) has(i identity) bool { return o.emails[i.Email] || o.names[i.Name] }

func (o owned) empty() bool { return len(o.emails) == 0 && len(o.names) == 0 }

// readIdentities reads the file that says which identities are yours.
//
// The prefixes are explicit because the two kinds behave differently — a name
// matches whatever the address is and an address whatever the name is — and
// guessing which one a line meant would be a silent mistake in a tool whose
// whole job is not to touch other people's commits.
func readIdentities(path string) (owned, error) {
	o := owned{emails: map[string]bool{}, names: map[string]bool{}}
	f, err := os.Open(path)
	if err != nil {
		return o, fmt.Errorf("cannot read the identities file: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for n := 1; s.Scan(); n++ {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		v = strings.TrimSpace(v)
		if !ok || v == "" {
			return o, fmt.Errorf("%s:%d: expected `email: …` or `name: …`, got %q", path, n, line)
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "email":
			o.emails[v] = true
		case "name":
			o.names[v] = true
		default:
			return o, fmt.Errorf("%s:%d: unknown kind %q; only `email` and `name`", path, n, k)
		}
	}
	if err := s.Err(); err != nil {
		return o, err
	}
	if o.empty() {
		return o, fmt.Errorf("%s names no identity: a rewrite that owns nothing would rewrite nothing", path)
	}
	return o, nil
}

// target is the identity everything of yours becomes.
//
// It takes git's configured identity by default, because overriding the identity
// is what put the wrong one in the history: the fix is not to override it again.
func target(g *git, flagValue string) (identity, error) {
	if flagValue != "" {
		open := strings.Index(flagValue, "<")
		if !strings.HasSuffix(flagValue, ">") || open < 1 {
			return identity{}, fmt.Errorf(`-to must be "Name <email>", got %q`, flagValue)
		}
		return identity{
			Name:  strings.TrimSpace(flagValue[:open]),
			Email: flagValue[open+1 : len(flagValue)-1],
		}, nil
	}
	name, errN := g.out("config", "user.name")
	mail, errE := g.out("config", "user.email")
	if errN != nil || errE != nil || name == "" || mail == "" {
		return identity{}, errors.New("git has no configured user.name and user.email, and -to was not given")
	}
	return identity{Name: name, Email: mail}, nil
}
