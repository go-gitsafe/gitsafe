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
//     The page does not document pagination because there is none: the endpoint
//     returns EVERY matching ref in one response and ignores page and per_page.
//     Measured 2026-11-03 on go-ansible/modules, which has 109 v* tags:
//     `?per_page=100&page=1` and `?per_page=100&page=2` both return the same
//     109 refs.
//
//     This was first written to send page/per_page anyway, on the reasoning that
//     a bounded loop could not be spun by a server that ignored them. It could
//     not be spun, but it could not FINISH either: a page of 109 is never the
//     short page that ends the walk, so every repository with 100 or more v*
//     tags ran the bound out and was refused with "more than 3000 tags" -- a
//     number computed from the bound rather than counted from anything. It
//     blocked go-ansible/modules (109), go-ansible/cli (176) and
//     go-ansible/playbook (177) within a day of shipping.
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

// maxTagBytes bounds the one response listTags reads. 8 MiB is roughly 90 000
// refs at the shape GitHub sends, which is far above any repository this tool
// is pointed at and far below anything that could exhaust memory.
const maxTagBytes = 8 << 20

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

// listTags returns the names of the repository's <prefix>v* tags (prefix is ""
// or "<dir>/"), full names, prefix included.
//
// ONE request, with no page or per_page: matching-refs is not paginated and
// this response is the whole list (see the measurement at the top of the file).
// Asking for a second page would return the same refs again, which is what the
// first version of this did thirty times before refusing.
//
// The body is still bounded. maxTagBytes is what stops a hostile or broken
// server streaming without end; a body cut at that limit ends mid-array and
// fails to decode, so a truncated list is an error rather than a short one
// silently treated as complete.
func listTags(token, repo, prefix string, b *budget) ([]string, error) {
	if why := b.low(); why != "" {
		return nil, fmt.Errorf("%s", why)
	}
	resp, err := api(token, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/git/matching-refs/tags/%sv", apiBase, repo, prefix), nil)
	if err != nil {
		return nil, err
	}
	b.noteREST(resp.Header)
	if resp.StatusCode != http.StatusOK {
		err := refused("listing the repository's tags", resp)
		resp.Body.Close()
		return nil, err
	}
	var refs []struct {
		Ref string `json:"ref"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, maxTagBytes)).Decode(&refs)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading the tags (over %d bytes is refused, not truncated): %w", maxTagBytes, err)
	}

	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, strings.TrimPrefix(r.Ref, "refs/tags/"))
	}
	return names, nil
}

// checkOrder says whether tag may be created. lower reports an accepted
// -allow-lower tag, which must not become Latest. A non-nil message is printed
// on stderr with the refusal.
//
// A nested module's tag <dir>/vX.Y.Z is ordered among the <dir>/v* tags ONLY: the
// root module's v* tags, and other modules', are another line of versions.
// prev is the highest existing tag of the same line below the new one (with its
// <dir>/ prefix), for the release notes of a nested module.
func checkOrder(token, repo, tag string, allowLower bool, b *budget) (lower bool, prev string, refusal string, err error) {
	dir, ver, _ := splitTag(tag)
	nv, perr := parseStrict(ver)
	if perr != nil {
		return false, "", perr.Error(), nil
	}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	names, err := listTags(token, repo, prefix, b)
	if err != nil {
		return false, "", "", err
	}
	line := make([]string, 0, len(names))
	for _, n := range names {
		if s, ok := strings.CutPrefix(n, prefix); ok {
			line = append(line, s)
		}
	}
	hi, found := highestRelease(line)
	if !found {
		return false, "", "", nil
	}
	if nv.cmp(hi) > 0 {
		return false, prefix + hi.String(), "", nil
	}
	if allowLower {
		return true, "", "", nil
	}
	if prefix != "" {
		repo += " (" + prefix + "v* tags)"
	}
	rel := "lower than"
	if nv.cmp(hi) == 0 {
		rel = "equal to"
	}
	next := func(v version) string { return prefix + v.String() }
	return false, "", fmt.Sprintf("%s is %s the highest release of %s, %s: GitHub would make it Latest only if it were the newest, "+
		"and `go install ...@latest` would keep resolving to %s, so the new code would be unreachable.\n"+
		"next versions: PATCH %s, MINOR %s, MAJOR %s\n"+
		"(a deliberate backport on a release branch: -allow-lower; it is then not marked Latest)",
		tag, rel, repo, prefix+hi.String(), prefix+hi.String(),
		next(version{hi[0], hi[1], hi[2] + 1}), next(version{hi[0], hi[1] + 1, 0}), next(version{hi[0] + 1, 0, 0})), nil
}
