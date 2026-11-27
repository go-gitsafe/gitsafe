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
//     exists. So the merged code is there and nobody downstream gets it.
//
// The gate refuses when ALL of these hold: the repository has at least one
// stable vX.Y.Z tag, has a go.mod at its root or below, and the pull request
// changes a non-test .go file, a go.mod or a go.sum. It names the exact
// `ghrelease OWNER/REPO N vX.Y.Z` command, the next PATCH over the semver
// maximum of ALL the tags (GitHub's tag listing is not in semver order).
//
// What it cannot read is not "no Go change": an unreadable tag list, file list
// or tree REFUSES, naming the cause.
//
// ghrelease merges through this command and passes --no-release (it IS the
// release), so it cannot be refused by its own gate.

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
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
	Tags     []string // every root v* tag, any order
	GoMod    bool     // a go.mod at the root or below
	Triggers []string // changed files that publish Go code (sorted)
}

// releaseFactsFor is the seam for the GitHub reads, so a test answers as GitHub
// would without a server.
var releaseFactsFor = readReleaseFacts

// highestStable is the semver maximum of the stable vX.Y.Z tags, and whether
// there is one. Pre-releases and non-semver names are ignored.
func highestStable(tags []string) (v [3]uint64, found bool) {
	for _, t := range tags {
		c, ok := stable(t)
		if !ok {
			continue
		}
		if !found || less(v, c) {
			v, found = c, true
		}
	}
	return v, found
}

func less(a, b [3]uint64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// stable parses exactly vMAJOR.MINOR.PATCH (numbers without leading zeros,
// semver.org items 2 and 9: no pre-release, no build).
func stable(tag string) (v [3]uint64, ok bool) {
	s, found := strings.CutPrefix(tag, "v")
	if !found {
		return v, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		if p == "" || len(p) > 9 || (len(p) > 1 && p[0] == '0') {
			return v, false
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// publishesGo says whether a changed file is one a downstream `go get` sees.
func publishesGo(name string) bool {
	base := path.Base(name)
	switch {
	case base == "go.mod", base == "go.sum":
		return true
	}
	return strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go")
}

// releaseRefusal is the verdict: "" when the pull request may be merged as it
// is, else the whole refusal text.
func releaseRefusal(repo string, number int, f releaseFacts) string {
	hi, found := highestStable(f.Tags)
	if !found || !f.GoMod || len(f.Triggers) == 0 {
		return ""
	}
	show := f.Triggers
	more := ""
	if len(show) > 5 {
		show, more = show[:5], fmt.Sprintf(" and %d more", len(f.Triggers)-5)
	}
	return fmt.Sprintf("%s is a tagged Go module (highest release v%d.%d.%d) and this pull request changes Go code (%s%s). "+
		"Merged without a tag, `go get` and the module proxy keep resolving @latest to v%d.%d.%d and never see it "+
		"(https://go.dev/ref/mod#versions, https://go.dev/ref/mod#vcs-version).\n"+
		"ghmerge: merge AND tag it in one command: ghrelease %s %d v%d.%d.%d\n"+
		"ghmerge: if a LATER pull request of a series carries the tag: ghmerge --no-release '<why>' %s %d",
		repo, hi[0], hi[1], hi[2], strings.Join(show, ", "), more, hi[0], hi[1], hi[2],
		repo, number, hi[0], hi[1], hi[2]+1,
		repo, number)
}

// readReleaseFacts asks GitHub. It stops asking as soon as the answer is known
// (no stable tag: nothing else is read), which spares a budget shared with every
// other session.
func readReleaseFacts(token, repo string, p *pr, number int) (releaseFacts, error) {
	var f releaseFacts
	var refs []struct {
		Ref string `json:"ref"`
	}
	// matching-refs is not paginated: one response is the whole list.
	if err := get(token, fmt.Sprintf("%s/repos/%s/git/matching-refs/tags/v", apiBase, repo), &refs); err != nil {
		return f, fmt.Errorf("listing the tags of %s: %w", repo, err)
	}
	for _, r := range refs {
		f.Tags = append(f.Tags, strings.TrimPrefix(r.Ref, "refs/tags/"))
	}
	if _, found := highestStable(f.Tags); !found {
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
			} else if x.Prev != "" && publishesGo(x.Prev) {
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
	for _, e := range tree.Tree {
		if path.Base(e.Path) == "go.mod" {
			f.GoMod = true
			break
		}
	}
	if !f.GoMod && tree.Truncated {
		return f, fmt.Errorf("the tree of %s at %s is truncated by GitHub and holds no go.mod in what was sent", repo, p.Head.SHA)
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
