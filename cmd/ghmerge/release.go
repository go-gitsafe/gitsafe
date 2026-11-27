package main

// THE RELEASE GATE: a pull request that changes Go code in a tagged module is
// not merged without a tag.
//
// Eight times (ghrelease-tool memory, last two on 2026-11-27: go-numerics/special
// #104-#106 and go-widgets/svg#50, both by sub-agents) a pull request was merged
// with ghmerge alone. The code was on main and invisible to `go get`.
//
// Why it is invisible. Sources, opened before this was written:
//
//   - https://go.dev/ref/mod#versions : a module version is a semantic version
//     tag, and `@latest` resolves to the highest release version.
//   - https://go.dev/ref/mod#vcs-version : for a module in a VCS repository the
//     version is the repository tag (vX.Y.Z); a commit with no tag is only
//     reachable as a pseudo-version, which no `go get -u` picks while a tag
//     exists. So the merged code is there and nobody downstream gets it. A module
//     in a subdirectory has its own tags, prefixed with that subdirectory and a
//     slash (gopls/v0.4.0): "each tag name must be prefixed with the module
//     subdirectory, followed by a slash". A module in a major version
//     subdirectory (sub/v2) is tagged sub/v2.x.y, so its subdirectory is sub.
//
// So the gate asks the question PER MODULE. Each changed file belongs to the
// module of the nearest go.mod above it (the module's own go.mod and go.sum
// included); that module's releases are the tags under its prefix, and only
// they decide whether the change is invisible. A repository whose only tags are
// sub/v1.2.0 is a tagged module for sub and not for the root: tags of one module
// say nothing about another's. (The first version of the gate read only the root
// v* tags, and let a change to a nested module through when that was all the
// tags it had.)
//
// The gate refuses when a changed file, non-test .go, go.mod or go.sum, belongs
// to a module that has at least one stable release. It names the exact
// `ghrelease OWNER/REPO N <prefix>vX.Y.Z` command, the next PATCH over the
// semver maximum of ALL the module's tags (GitHub's tag listing is not in semver
// order). ghrelease tags a prefixed module (its nested-module check reads the
// go.mod at the merge commit), one tag per merge; a second module changed by the
// same pull request is tagged by running ghrelease again, which tags the merge
// commit of an already merged pull request.
//
// What it cannot read is not "no Go change": an unreadable tag list, file list
// or tree REFUSES, naming the cause. A tree GitHub truncated cannot say which
// go.mod owns a file, so it refuses too.
//
// Known limit, refused rather than guessed: a module and a major version
// subdirectory below it that are both released (sub and sub/v2) share the
// prefix sub/, and the module at sub is judged by the highest tag under it; the
// refusal then names a version of the other major line, and ghrelease rejects
// it against the go.mod it reads. The escape is --no-release.
//
// ghrelease merges through this command and passes --no-release (it IS the
// release), so it cannot be refused by its own gate.

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/go-gitsafe/gitsafe/relver"
)

// releaseFiles is how many files GitHub lists for one pull request at most
// (3000, https://docs.github.com/en/rest/pulls/pulls#list-pull-requests-files),
// at 100 a page.
const (
	filesPerPage = 100
	filesMax     = 3000
)

// releaseFacts is what the gate needs to know, read from GitHub.
type releaseFacts struct {
	Tags     []string // every tag, any order, a nested module's with its <dir>/ prefix
	Modules  []string // the directory of every go.mod in the tree ("" is the root)
	Triggers []string // changed files that publish Go code (sorted)
}

// releaseFactsFor is the seam for the GitHub reads, so a test answers as GitHub
// would without a server.
var releaseFactsFor = readReleaseFacts

// publishesGo says whether a changed file is one a downstream `go get` sees.
func publishesGo(name string) bool {
	base := path.Base(name)
	switch {
	case base == "go.mod", base == "go.sum":
		return true
	}
	return strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go")
}

// ownerOf is the module a file belongs to: the nearest go.mod above it, the
// file's own directory included. ok is false for a file under no go.mod.
func ownerOf(file string, modules []string) (dir string, ok bool) {
	best := -1
	for _, m := range modules {
		if m == "" || strings.HasPrefix(file, m+"/") {
			if len(m) > best {
				dir, best, ok = m, len(m), true
			}
		}
	}
	return dir, ok
}

// majorDir reads a path element vN (N >= 2, no leading zero) as its N.
func majorDir(elem string) (uint64, bool) {
	s, found := strings.CutPrefix(elem, "v")
	if !found || len(s) == 0 || len(s) > 9 || s[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 32)
	return n, err == nil && n >= 2
}

// moduleLine is where the tags of the module in dir are: the prefix ("" or
// "<parent>/") and, for a module in a major version subdirectory, that major
// (else 0: any). The major version suffix is not part of the module's
// subdirectory (go.dev/ref/mod#vcs-version: sub/v2 is tagged sub/v2.x.y).
func moduleLine(dir string) (prefix string, major uint64) {
	if dir == "" {
		return "", 0
	}
	i := strings.LastIndex(dir, "/")
	if n, ok := majorDir(dir[i+1:]); ok {
		return dir[:i+1], n // i is -1 for a root-level v2: the prefix is ""
	}
	return dir + "/", 0
}

// highestOf is the semver maximum of the stable tags in one module's line.
func highestOf(tags []string, prefix string, major uint64) (relver.Version, bool) {
	var names []string
	for _, n := range relver.Line(tags, prefix) {
		if major != 0 {
			if v, _, ok := relver.ParseCore(n); !ok || v[0] != major {
				continue
			}
		}
		names = append(names, n)
	}
	return relver.Highest(names)
}

// anyStable says whether any tag, of any module, is a stable release.
func anyStable(tags []string) bool {
	for _, t := range tags {
		if _, ver, ok := relver.SplitTag(t); ok {
			if _, found := relver.Highest([]string{ver}); found {
				return true
			}
		}
	}
	return false
}

// release is one tagged module a pull request changes.
type release struct {
	dir    string
	prefix string
	hi     relver.Version
	files  []string
}

func (r release) name() string {
	if r.dir == "" {
		return "the root module"
	}
	return "module " + r.dir
}

// tagged is the tagged modules the pull request changes, by directory.
func tagged(f releaseFacts) []release {
	by := map[string][]string{}
	for _, file := range f.Triggers {
		if dir, ok := ownerOf(file, f.Modules); ok {
			by[dir] = append(by[dir], file)
		}
	}
	dirs := make([]string, 0, len(by))
	for d := range by {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var out []release
	for _, d := range dirs {
		prefix, major := moduleLine(d)
		if hi, found := highestOf(f.Tags, prefix, major); found {
			out = append(out, release{dir: d, prefix: prefix, hi: hi, files: by[d]})
		}
	}
	return out
}

// releaseRefusal is the verdict: "" when the pull request may be merged as it
// is, else the whole refusal text.
func releaseRefusal(repo string, number int, f releaseFacts) string {
	rels := tagged(f)
	if len(rels) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s is a tagged Go repository and this pull request changes Go code in it:\n", repo)
	for _, r := range rels {
		show, more := r.files, ""
		if len(show) > 5 {
			show, more = show[:5], fmt.Sprintf(" and %d more", len(r.files)-5)
		}
		fmt.Fprintf(&b, "  - %s (highest release %s%s): %s%s\n", r.name(), r.prefix, r.hi, strings.Join(show, ", "), more)
	}
	b.WriteString("Merged without a tag, `go get` and the module proxy keep resolving @latest to the highest release and never see it " +
		"(https://go.dev/ref/mod#versions, https://go.dev/ref/mod#vcs-version).\n")
	for i, r := range rels {
		tag := r.prefix + r.hi.NextPatch().String()
		if _, _, ok := relver.SplitTag(tag); !ok {
			fmt.Fprintf(&b, "ghmerge: %s: ghrelease cannot tag it, %q is not a tag name ghrelease accepts "+
				"(a nested module's tag is <dir>/vX.Y.Z): tag it by hand, then ghmerge --no-release '<why>' %s %d\n",
				r.name(), tag, repo, number)
			continue
		}
		switch {
		case i == 0:
			fmt.Fprintf(&b, "ghmerge: merge AND tag it in one command: ghrelease %s %d %s\n", repo, number, tag)
		default:
			fmt.Fprintf(&b, "ghmerge: %s, once merged (ghrelease tags one module per merge, and tags the merge commit of an already merged pull request): ghrelease %s %d %s\n",
				r.name(), repo, number, tag)
		}
	}
	fmt.Fprintf(&b, "ghmerge: if a LATER pull request of a series carries the tag: ghmerge --no-release '<why>' %s %d", repo, number)
	return b.String()
}

// readReleaseFacts asks GitHub. It stops asking as soon as the answer is known
// (no stable tag: nothing else is read), which spares a budget shared with every
// other session.
func readReleaseFacts(token, repo string, p *pr, number int) (releaseFacts, error) {
	var f releaseFacts
	var refs []struct {
		Ref string `json:"ref"`
	}
	// matching-refs is not paginated: one response is the whole list. "tags" and
	// not "tags/v": a nested module's tags are <dir>/vX.Y.Z, which a "v" prefix
	// would never list.
	if err := get(token, fmt.Sprintf("%s/repos/%s/git/matching-refs/tags", apiBase, repo), &refs); err != nil {
		return f, fmt.Errorf("listing the tags of %s: %w", repo, err)
	}
	for _, r := range refs {
		if name, ok := strings.CutPrefix(r.Ref, "refs/tags/"); ok {
			f.Tags = append(f.Tags, name)
		}
	}
	if !anyStable(f.Tags) {
		return f, nil
	}

	seen := 0
	for page := 1; page <= filesMax/filesPerPage; page++ {
		var files []struct {
			Name string `json:"filename"`
			Prev string `json:"previous_filename"`
		}
		url := fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=%d&page=%d", apiBase, repo, number, filesPerPage, page)
		if err := get(token, url, &files); err != nil {
			return f, fmt.Errorf("listing the files of %s#%d: %w", repo, number, err)
		}
		for _, x := range files {
			if publishesGo(x.Name) {
				f.Triggers = append(f.Triggers, x.Name)
			}
			// A file moved out of a module changes that module too.
			if x.Prev != "" && publishesGo(x.Prev) {
				f.Triggers = append(f.Triggers, x.Prev)
			}
		}
		seen += len(files)
		if len(files) < filesPerPage {
			break
		}
	}
	// A file list shorter than the pull request says it is, or empty for a pull
	// request that has changes, is a list that could not be read in full: what is
	// missing may be the .go file.
	if len(f.Triggers) == 0 && seen < p.ChangedFiles {
		return f, fmt.Errorf("the file list of %s#%d holds %d of its %d changed files (GitHub lists at most %d)",
			repo, number, seen, p.ChangedFiles, filesMax)
	}
	sort.Strings(f.Triggers)
	if len(f.Triggers) == 0 {
		return f, nil
	}

	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
		} `json:"tree"`
	}
	if err := get(token, fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1", apiBase, repo, p.Head.SHA), &tree); err != nil {
		return f, fmt.Errorf("listing the tree of %s at %s: %w", repo, p.Head.SHA, err)
	}
	// Which go.mod owns a file is read from the whole tree: a go.mod missing from
	// a truncated one would hand its files to the module above.
	if tree.Truncated {
		return f, fmt.Errorf("the tree of %s at %s is truncated by GitHub, so it cannot say which go.mod owns a changed file", repo, p.Head.SHA)
	}
	for _, e := range tree.Tree {
		if path.Base(e.Path) == "go.mod" {
			dir := path.Dir(e.Path)
			if dir == "." {
				dir = ""
			}
			f.Modules = append(f.Modules, dir)
		}
	}
	return f, nil
}

// checkRelease is the gate as execute calls it: "" to go on, else the reason to
// refuse. A read that fails refuses too.
func checkRelease(token, repo string, p *pr, number int) string {
	f, err := releaseFactsFor(token, repo, p, number)
	if err != nil {
		return fmt.Sprintf("the release gate could not read what it needs, and an unread answer is not \"no Go change\": %v\n"+
			"ghmerge: retry, or pass --no-release '<why>' to merge without a tag", err)
	}
	return releaseRefusal(repo, number, f)
}
