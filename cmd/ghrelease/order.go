package main

// Refusing a tag that is not above the repository's highest release.
//
// On 2026-11-02 `ghrelease go-llmflow/bench 17 v0.7.1` ran while the repository
// already had v0.9.0. The tag was created and GitHub made it Latest, so
// `go install ...@latest` kept resolving to v0.9.0 and the new code was
// unreachable; it had to be re-released as v0.9.1 on the same commit.
//
// Sources, opened before this was written:
//
//   - Semantic Versioning 2.0.0, https://semver.org/ : items 2 (X.Y.Z are
//     non-negative integers without leading zeroes), 9 (a pre-release has a lower
//     precedence than its normal version) and 11 (precedence is major, minor,
//     patch, then pre-release; build metadata is ignored).
//   - GitHub REST, "List matching references",
//     GET /repos/{owner}/{repo}/git/matching-refs/{ref}, https://docs.github.com/en/rest/git/refs#list-matching-references
//     (the page does not document pagination; page/per_page are sent and the
//     loop is bounded, so a server that ignores them cannot spin it).
//
// The NEW tag must be exactly vMAJOR.MINOR.PATCH. Existing tags that carry a
// pre-release are ignored: GitHub's Latest never points at one, so they cannot
// make a stable tag unreachable (semver item 9 puts v1.0.0-rc.1 below v1.0.0
// anyway). Tags that are not semver are ignored too.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxTagPages = 30

// orderCheck is a seam, so a test can put the old behaviour back (a planted control).
var orderCheck = checkOrder

// version is a stable semantic version.
type version [3]uint64

func (v version) String() string { return fmt.Sprintf("v%d.%d.%d", v[0], v[1], v[2]) }

func (v version) cmp(o version) int {
	for i := range v {
		switch {
		case v[i] < o[i]:
			return -1
		case v[i] > o[i]:
			return 1
		}
	}
	return 0
}

// parseCore reads "vX.Y.Z" with no leading zeros. rest is what follows Z
// ("" or "-pre" or "+build").
func parseCore(tag string) (v version, rest string, ok bool) {
	s, found := strings.CutPrefix(tag, "v")
	if !found {
		return v, "", false
	}
	i := strings.IndexAny(s, "-+")
	core := s
	if i >= 0 {
		core, rest = s[:i], s[i:]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, "", false
	}
	for k, p := range parts {
		if p == "" || len(p) > 9 || (len(p) > 1 && p[0] == '0') {
			return v, "", false
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return v, "", false
		}
		v[k] = n
	}
	return v, rest, true
}

// parseStrict accepts only vMAJOR.MINOR.PATCH.
func parseStrict(tag string) (version, error) {
	v, rest, ok := parseCore(tag)
	if !ok || rest != "" {
		return v, fmt.Errorf("%q is not vMAJOR.MINOR.PATCH (semver.org items 2 and 9: numbers without leading zeros, no pre-release or build)", tag)
	}
	return v, nil
}

// highestRelease is the highest stable tag among names, and whether there is one.
func highestRelease(names []string) (best version, found bool) {
	for _, n := range names {
		v, rest, ok := parseCore(n)
		if !ok || strings.HasPrefix(rest, "-") { // not semver, or a pre-release
			continue
		}
		if !found || v.cmp(best) > 0 {
			best, found = v, true
		}
	}
	return best, found
}

// listTags returns the names of the repository's v* tags.
func listTags(token, repo string, b *budget) ([]string, error) {
	var names []string
	for page := 1; page <= maxTagPages; page++ {
		if why := b.low(); why != "" {
			return nil, fmt.Errorf("%s", why)
		}
		resp, err := api(token, http.MethodGet,
			fmt.Sprintf("%s/repos/%s/git/matching-refs/tags/v?per_page=100&page=%d", apiBase, repo, page), nil)
		if err != nil {
			return nil, err
		}
		var refs []struct {
			Ref string `json:"ref"`
		}
		b.noteREST(resp.Header)
		if resp.StatusCode != http.StatusOK {
			err := refused("listing the repository's tags", resp)
			resp.Body.Close()
			return nil, err
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&refs)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the tags: %w", err)
		}
		for _, r := range refs {
			names = append(names, strings.TrimPrefix(r.Ref, "refs/tags/"))
		}
		if len(refs) < 100 {
			return names, nil
		}
	}
	return nil, fmt.Errorf("more than %d tags: too many to order here", maxTagPages*100)
}

// checkOrder says whether tag may be created. lower reports an accepted
// -allow-lower tag, which must not become Latest. A non-nil message is printed
// on stderr with the refusal.
func checkOrder(token, repo, tag string, allowLower bool, b *budget) (lower bool, refusal string, err error) {
	nv, perr := parseStrict(tag)
	if perr != nil {
		return false, perr.Error(), nil
	}
	names, err := listTags(token, repo, b)
	if err != nil {
		return false, "", err
	}
	hi, found := highestRelease(names)
	if !found || nv.cmp(hi) > 0 {
		return false, "", nil
	}
	if allowLower {
		return true, "", nil
	}
	rel := "lower than"
	if nv.cmp(hi) == 0 {
		rel = "equal to"
	}
	next := func(v version) string { return v.String() }
	return false, fmt.Sprintf("%s is %s the highest release of %s, %s: GitHub would make it Latest only if it were the newest, "+
		"and `go install ...@latest` would keep resolving to %s, so the new code would be unreachable.\n"+
		"next versions: PATCH %s, MINOR %s, MAJOR %s\n"+
		"(a deliberate backport on a release branch: -allow-lower; it is then not marked Latest)",
		tag, rel, repo, hi, hi,
		next(version{hi[0], hi[1], hi[2] + 1}), next(version{hi[0], hi[1] + 1, 0}), next(version{hi[0] + 1, 0, 0})), nil
}
