// Package relver reads release tags: vMAJOR.MINOR.PATCH, and a nested Go
// module's <dir>/vMAJOR.MINOR.PATCH.
//
// ghrelease (which orders a new tag against the existing ones) and ghmerge (whose
// release gate names the next tag) each carried their own copy of this parse;
// two copies of a rule that decides which version is the newest are two chances
// to disagree about it, so there is one.
//
// Sources, opened before this was written:
//
//   - Semantic Versioning 2.0.0, https://semver.org/ : items 2 (X.Y.Z are
//     non-negative integers without leading zeroes), 9 (a pre-release has a lower
//     precedence than its normal version) and 11 (precedence is major, minor,
//     patch, then pre-release; build metadata is ignored).
//   - The Go Modules Reference, https://go.dev/ref/mod#vcs-version : a module in
//     a subdirectory of the repository has its tags prefixed with that
//     subdirectory and a slash (gopls/v0.4.0).
//
// Pre-release tags are never "highest": GitHub's Latest never points at one, and
// semver item 9 puts v1.0.0-rc.1 below v1.0.0 anyway.
package relver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a stable semantic version.
type Version [3]uint64

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v[0], v[1], v[2]) }

// Cmp is -1, 0 or 1 as v is below, equal to or above o.
func (v Version) Cmp(o Version) int {
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

// NextPatch is the version one PATCH above v.
func (v Version) NextPatch() Version { return Version{v[0], v[1], v[2] + 1} }

// NextMinor is the version one MINOR above v.
func (v Version) NextMinor() Version { return Version{v[0], v[1] + 1, 0} }

// NextMajor is the version one MAJOR above v.
func (v Version) NextMajor() Version { return Version{v[0] + 1, 0, 0} }

// ParseCore reads "vX.Y.Z" with no leading zeros. rest is what follows Z
// ("" or "-pre" or "+build").
func ParseCore(tag string) (v Version, rest string, ok bool) {
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

// ParseStrict accepts only vMAJOR.MINOR.PATCH.
func ParseStrict(tag string) (Version, error) {
	v, rest, ok := ParseCore(tag)
	if !ok || rest != "" {
		return v, fmt.Errorf("%q is not vMAJOR.MINOR.PATCH (semver.org items 2 and 9: numbers without leading zeros, no pre-release or build)", tag)
	}
	return v, nil
}

// Highest is the highest stable tag among names (each "vX.Y.Z", no directory),
// and whether there is one. Pre-releases and non-semver names are ignored.
func Highest(names []string) (best Version, found bool) {
	for _, n := range names {
		v, rest, ok := ParseCore(n)
		if !ok || strings.HasPrefix(rest, "-") { // not semver, or a pre-release
			continue
		}
		if !found || v.Cmp(best) > 0 {
			best, found = v, true
		}
	}
	return best, found
}

// SplitTag separates a tag into its module subdirectory ("" for the root
// module) and the version part, which must start with v. ok is false for a tag
// with no v or with an empty, "." or ".." directory element.
func SplitTag(tag string) (dir, ver string, ok bool) {
	i := strings.LastIndex(tag, "/")
	if i < 0 {
		if !strings.HasPrefix(tag, "v") {
			return "", "", false
		}
		return "", tag, true
	}
	dir, ver = tag[:i], tag[i+1:]
	if !strings.HasPrefix(ver, "v") {
		return "", "", false
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, " \t\\:?*[~^@{") {
			return "", "", false
		}
	}
	return dir, ver, true
}

// Line is the stable versions of one module's tags: those under prefix ("" for
// the root module, else "<dir>/"). Names that do not start with the prefix are
// another module's and are left out.
func Line(tags []string, prefix string) []string {
	line := make([]string, 0, len(tags))
	for _, n := range tags {
		if s, ok := strings.CutPrefix(n, prefix); ok && !strings.Contains(s, "/") {
			line = append(line, s)
		}
	}
	return line
}
