package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// git runs git in one repository.
type git struct{ dir string }

func (g *git) cmd(args ...string) *exec.Cmd {
	c := exec.Command("git", args...)
	c.Dir = g.dir
	return c
}

func (g *git) out(args ...string) (string, error) {
	var out, errb bytes.Buffer
	c := g.cmd(args...)
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

func (g *git) lines(args ...string) ([]string, error) {
	s, err := g.out(args...)
	if err != nil || s == "" {
		return nil, err
	}
	return strings.Split(s, "\n"), nil
}

// object reads one object whole. Raw bytes, so nothing is reinterpreted.
func (g *git) object(kind, sha string) ([]byte, error) {
	var out, errb bytes.Buffer
	c := g.cmd("cat-file", kind, sha)
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("cat-file %s %s: %v: %s", kind, sha, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// write stores an object of the given kind and returns its id.
func (g *git) write(kind string, body []byte) (string, error) {
	var out, errb bytes.Buffer
	c := g.cmd("hash-object", "-t", kind, "-w", "--stdin")
	c.Stdin, c.Stdout, c.Stderr = bytes.NewReader(body), &out, &errb
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("hash-object -t %s: %v: %s", kind, err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// stamped is an identity with the timestamp git stores beside it, kept as the
// exact bytes git wrote: "1791707533 +0200". Parsing it into a time and printing
// it back is how an offset gets normalised and a date silently moves.
type stamped struct {
	identity
	When string
}

func (s stamped) line(kind string) string {
	return kind + " " + s.Name + " <" + s.Email + "> " + s.When
}

// commit is a commit object taken apart far enough to rebuild it unchanged.
type commit struct {
	SHA       string
	Tree      string
	Parents   []string
	Author    stamped
	Committer stamped
	// Other headers in the order git wrote them, excluding tree, parent, author,
	// committer and any signature.
	Other  []string
	Signed bool
	Body   []byte
}

// parseCommit takes a raw commit object apart.
//
// ⛔ A header can continue over several lines, each continuation starting with a
// space — a PGP signature is the common case, and reading line by line without
// that rule turns its body into bogus headers.
func parseCommit(sha string, raw []byte) (*commit, error) {
	head, body, found := bytes.Cut(raw, []byte("\n\n"))
	if !found {
		// A commit with no message has no blank line; its headers are all of it.
		head, body = bytes.TrimRight(raw, "\n"), nil
	}
	c := &commit{SHA: sha, Body: body}
	var cur []string
	flush := func() error {
		if len(cur) == 0 {
			return nil
		}
		whole := strings.Join(cur, "\n")
		cur = nil
		switch {
		case strings.HasPrefix(whole, "tree "):
			c.Tree = strings.TrimPrefix(whole, "tree ")
		case strings.HasPrefix(whole, "parent "):
			c.Parents = append(c.Parents, strings.TrimPrefix(whole, "parent "))
		case strings.HasPrefix(whole, "author "):
			s, err := parseStamped(strings.TrimPrefix(whole, "author "))
			if err != nil {
				return fmt.Errorf("%s: author: %w", sha, err)
			}
			c.Author = s
		case strings.HasPrefix(whole, "committer "):
			s, err := parseStamped(strings.TrimPrefix(whole, "committer "))
			if err != nil {
				return fmt.Errorf("%s: committer: %w", sha, err)
			}
			c.Committer = s
		case strings.HasPrefix(whole, "gpgsig "), strings.HasPrefix(whole, "gpgsig-sha256 "):
			// A signature covers the parent ids, so it cannot survive a rewrite.
			// Dropped, and counted, rather than carried forward invalid.
			c.Signed = true
		default:
			c.Other = append(c.Other, whole)
		}
		return nil
	}
	for _, line := range strings.Split(string(head), "\n") {
		if strings.HasPrefix(line, " ") && len(cur) > 0 {
			cur = append(cur, line)
			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		cur = []string{line}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if c.Tree == "" || c.Author.Email == "" && c.Author.Name == "" {
		return nil, fmt.Errorf("%s: not a commit object this can rebuild", sha)
	}
	return c, nil
}

// parseStamped splits "Name <email> 1791707533 +0200".
func parseStamped(s string) (stamped, error) {
	open := strings.LastIndex(s, " <")
	close := strings.Index(s[max(open, 0):], "> ")
	if open < 0 || close < 0 {
		return stamped{}, fmt.Errorf("cannot read %q", s)
	}
	close += max(open, 0)
	return stamped{
		identity: identity{Name: s[:open], Email: s[open+2 : close]},
		When:     s[close+2:],
	}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// bytes rebuilds the commit object.
func (c *commit) bytes(parents []string) []byte {
	b := &bytes.Buffer{}
	fmt.Fprintf(b, "tree %s\n", c.Tree)
	for _, p := range parents {
		fmt.Fprintf(b, "parent %s\n", p)
	}
	b.WriteString(c.Author.line("author") + "\n")
	b.WriteString(c.Committer.line("committer") + "\n")
	for _, h := range c.Other {
		b.WriteString(h + "\n")
	}
	b.WriteString("\n")
	b.Write(c.Body)
	return b.Bytes()
}

// ref is a branch or a tag, and for an annotated tag the commit under it.
//
// ⛔ The two object ids have to be read as two fields. Reading them as one
// string is how the shell version this replaces silently left every annotated
// tag behind, still pointing at a commit that carried the wrong address.
type ref struct {
	Name    string
	Kind    string // "commit" or "tag"
	Object  string // the ref's own object: the commit, or the tag object
	Pointee string // for an annotated tag, the commit under it
	TreeWas string
}

func (r ref) commitSHA() string {
	if r.Kind == "tag" {
		return r.Pointee
	}
	return r.Object
}

func (g *git) refs() ([]ref, error) {
	out, err := g.lines("for-each-ref",
		"--format=%(refname)%09%(objecttype)%09%(objectname)%09%(*objectname)",
		"refs/heads", "refs/tags")
	if err != nil {
		return nil, err
	}
	var rs []ref
	for _, l := range out {
		f := strings.Split(l, "\t")
		if len(f) < 4 {
			return nil, fmt.Errorf("cannot read ref line %q", l)
		}
		r := ref{Name: f[0], Kind: f[1], Object: f[2], Pointee: f[3]}
		if r.TreeWas, err = g.out("rev-parse", r.Name+"^{tree}"); err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	return rs, nil
}

// lock claims the repository for one rewrite.
func lock(g *git) (func(), error) {
	dir, err := g.out("rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "reauthor.lock")
	if err := os.Mkdir(p, 0o700); err != nil {
		return nil, fmt.Errorf("%s exists — another rewrite is running here, or one was killed.\n"+
			"  If nothing is running (check: pgrep -fl reauthor), remove it: rmdir %q", p, p)
	}
	os.WriteFile(filepath.Join(p, "pid"), []byte(fmt.Sprint(os.Getpid())), 0o600)
	return func() { os.RemoveAll(p) }, nil
}

func rewrite(g *git, own owned, want identity, dry bool, stdout, stderr io.Writer) (int, error) {
	if dirty, err := g.out("status", "--porcelain", "--untracked-files=no"); err != nil {
		return 1, err
	} else if dirty != "" {
		return 1, errors.New("there are uncommitted changes to tracked files; commit or stash them first")
	}

	if !dry {
		unlock, err := lock(g)
		if err != nil {
			return 1, err
		}
		defer unlock()
	}

	refs, err := g.refs()
	if err != nil {
		return 1, err
	}
	if len(refs) == 0 {
		return 1, errors.New("no branches and no tags: nothing would be published, so nothing to rewrite")
	}

	order, err := g.lines("rev-list", "--reverse", "--topo-order", "--branches", "--tags")
	if err != nil {
		return 1, err
	}

	newOf := map[string]string{}
	var mine, others, signed int
	for _, sha := range order {
		raw, err := g.object("commit", sha)
		if err != nil {
			return 1, err
		}
		c, err := parseCommit(sha, raw)
		if err != nil {
			return 1, err
		}
		if c.Signed {
			signed++
		}
		if own.has(c.Author.identity) {
			c.Author.identity = want
			mine++
		} else {
			others++
		}
		if own.has(c.Committer.identity) {
			c.Committer.identity = want
		}
		parents := make([]string, 0, len(c.Parents))
		for _, p := range c.Parents {
			if np, ok := newOf[p]; ok {
				parents = append(parents, np)
			} else {
				parents = append(parents, p)
			}
		}
		if dry {
			newOf[sha] = sha
			continue
		}
		next, err := g.write("commit", c.bytes(parents))
		if err != nil {
			return 1, err
		}
		newOf[sha] = next
	}

	if dry {
		fmt.Fprintf(stdout, "%d commits: %d yours, %d left alone; %d signature(s) would be dropped\n",
			len(order), mine, others, signed)
		fmt.Fprintf(stdout, "  would rewrite your commits to %s\n", want)
		fmt.Fprintln(stdout, "  nothing was written")
		return 0, nil
	}

	moved, left := 0, 0
	for _, r := range refs {
		next, ok := newOf[r.commitSHA()]
		if !ok {
			fmt.Fprintf(stderr, "reauthor: %s points at %s, which was not rebuilt — left alone\n",
				r.Name, r.commitSHA())
			left++
			continue
		}
		if r.Kind == "tag" {
			if err := g.retag(r, next, own, want); err != nil {
				return 1, err
			}
		} else if _, err := g.out("update-ref", r.Name, next); err != nil {
			return 1, err
		}
		moved++
	}

	if _, err := g.out("reset", "--hard", "HEAD"); err != nil {
		return 1, err
	}

	// ⛔ Every ref's tree, not only HEAD's. A rewrite that moved one branch onto
	// the wrong commit would leave HEAD's tree intact and still be wrong.
	after, err := g.refs()
	if err != nil {
		return 1, err
	}
	was := map[string]string{}
	for _, r := range refs {
		was[r.Name] = r.TreeWas
	}
	for _, r := range after {
		if old, ok := was[r.Name]; ok && old != r.TreeWas {
			return 1, fmt.Errorf("THE TREE OF %s CHANGED (%s -> %s) — do not push, something is wrong",
				r.Name, old[:8], r.TreeWas[:8])
		}
	}

	// What matters is what will be published: branches and tags.
	census, err := g.lines("log", "--branches", "--tags", "--format=%an <%ae>%x09%cn <%ce>")
	if err != nil {
		return 1, err
	}
	counts := map[string]int{}
	var stillMine []string
	for _, l := range census {
		for _, who := range strings.Split(l, "\t") {
			counts[who]++
			if i, err := parseWho(who); err == nil && own.has(i) {
				stillMine = append(stillMine, who)
			}
		}
	}
	if len(stillMine) > 0 {
		sort.Strings(stillMine)
		return 1, fmt.Errorf("%d of yours rewritten, %d left alone, BUT STILL PRESENT: %s",
			mine, others, strings.Join(uniq(stillMine), ", "))
	}

	fmt.Fprintf(stdout, "%d commits: %d of yours rewritten to %s, %d untouched\n",
		len(order), mine, want, others)
	fmt.Fprintf(stdout, "%d refs moved, %d left alone, %d signature(s) dropped, every tree unchanged\n",
		moved, left, signed)
	fmt.Fprintln(stdout, "who is in it now:")
	for _, line := range byCount(counts) {
		fmt.Fprintln(stdout, "   ", line)
	}
	fmt.Fprintln(stdout, "nothing is pushed. Publishing a rewrite is an act taken on purpose:")
	fmt.Fprintln(stdout, "    gitpush --force origin --all")
	fmt.Fprintln(stdout, "    gitpush --force origin --tags")
	fmt.Fprintln(stdout, "  and do NOT fetch in between: `git fetch --tags` brings the old tags back.")
	return 0, nil
}

// retag rebuilds an annotated tag on the new commit.
//
// ⛔ It rewrites the tag OBJECT rather than running `git tag -a -m`, which the
// shell version did: that keeps only the subject, so every annotated tag with a
// body lost everything after its first line. The tagger's date is kept for the
// same reason.
func (g *git) retag(r ref, onto string, own owned, want identity) error {
	raw, err := g.object("tag", r.Object)
	if err != nil {
		return err
	}
	head, body, found := bytes.Cut(raw, []byte("\n\n"))
	if !found {
		head, body = bytes.TrimRight(raw, "\n"), nil
	}
	out := &bytes.Buffer{}
	for _, line := range strings.Split(string(head), "\n") {
		switch {
		case strings.HasPrefix(line, "object "):
			fmt.Fprintf(out, "object %s\n", onto)
		case strings.HasPrefix(line, "tagger "):
			s, err := parseStamped(strings.TrimPrefix(line, "tagger "))
			if err != nil {
				return fmt.Errorf("%s: tagger: %w", r.Name, err)
			}
			if own.has(s.identity) {
				s.identity = want
			}
			out.WriteString(s.line("tagger") + "\n")
		default:
			out.WriteString(line + "\n")
		}
	}
	out.WriteString("\n")
	out.Write(body)
	next, err := g.write("tag", out.Bytes())
	if err != nil {
		return err
	}
	_, err = g.out("update-ref", r.Name, next)
	return err
}

func parseWho(s string) (identity, error) {
	open := strings.LastIndex(s, " <")
	if open < 0 || !strings.HasSuffix(s, ">") {
		return identity{}, fmt.Errorf("cannot read %q", s)
	}
	return identity{Name: s[:open], Email: s[open+2 : len(s)-1]}, nil
}

func uniq(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || in[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func byCount(m map[string]int) []string {
	type kv struct {
		k string
		n int
	}
	var all []kv
	for k, n := range m {
		all = append(all, kv{k, n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].k < all[j].k
	})
	out := make([]string, 0, len(all))
	for _, e := range all {
		out = append(out, fmt.Sprintf("%5d  %s", e.n, e.k))
	}
	return out
}
