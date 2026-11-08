package main

// A nested Go module's tag: <dir>/vX.Y.Z (go-gitsafe/gitsafe#90, #62).
//
// Source, opened before this was written: The Go Modules Reference, "Mapping
// versions to commits", https://go.dev/ref/mod#vcs-version :
//
//   - "If a module is defined in the repository root directory or in a major
//     version subdirectory of the root directory, then each version tag name is
//     equal to the corresponding version."
//   - "If a module is defined in a subdirectory within the repository, that is,
//     the module subdirectory portion of the module path is not empty, then each
//     tag name must be prefixed with the module subdirectory, followed by a
//     slash. For example, the module golang.org/x/tools/gopls is defined in the
//     gopls subdirectory ... The version v0.4.0 of that module must have the tag
//     named gopls/v0.4.0 in that repository."
//   - "The major version number of a semantic version tag must be consistent
//     with the module path's major version suffix (if any)."
//   - "Once a tag is created, it should not be deleted or changed to a different
//     revision."
//
// and section "Module paths": the module subdirectory does NOT include the major
// version suffix, and a module "golang.org/x/repo/sub/v2" "could be in the /sub
// or /sub/v2 subdirectory". So for the tag sub/v2.1.0 the go.mod is at
// sub/go.mod or at sub/v2/go.mod, and its path ends in /sub/v2 either way.
//
// What follows from it, and what is refused:
//
//   - the tag is <dir>/vMAJOR.MINOR.PATCH; <dir> is the module subdirectory;
//   - at the MERGE COMMIT the go.mod of the module must exist and declare a path
//     ending in /<dir> (major 0 or 1) or /<dir>/vN (N >= 2, N == the tag's major);
//   - ordering is among the <dir>/v* tags only, never the root module's.

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// splitTag separates a tag into its module subdirectory ("" for the root
// module) and the version part, which must start with v. ok is false for a tag
// with no v or with an empty, "." or ".." directory element.
func splitTag(tag string) (dir, ver string, ok bool) {
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

// moduleDirPath reads the module path from a go.mod: the first module
// directive, plain, quoted, or in a block.
func moduleDirPath(gomod string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(gomod))
	inBlock := false
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		var arg string
		switch {
		case inBlock:
			if line == ")" {
				inBlock = false
				continue
			}
			arg = line
		case strings.HasPrefix(line, "module") && (len(line) == 6 || strings.ContainsRune(" \t(\"`", rune(line[6]))):
			rest := strings.TrimSpace(line[6:])
			if rest == "(" {
				inBlock = true
				continue
			}
			arg = rest
		default:
			continue
		}
		if arg == "" {
			continue
		}
		if len(arg) >= 2 && (arg[0] == '"' || arg[0] == '`') && arg[len(arg)-1] == arg[0] {
			arg = arg[1 : len(arg)-1]
		}
		if arg != "" {
			return arg, true
		}
	}
	return "", false
}

// pathMajor is the N of a trailing /vN (N >= 2) in a module path, or 0.
func pathMajor(modPath string) (base string, major uint64) {
	i := strings.LastIndex(modPath, "/")
	if i < 0 || !strings.HasPrefix(modPath[i+1:], "v") {
		return modPath, 0
	}
	n, err := strconv.ParseUint(modPath[i+2:], 10, 32)
	if err != nil || n < 2 || modPath[i+2] == '0' {
		return modPath, 0
	}
	return modPath[:i], n
}

// judgeNestedModule applies the rules to what was found. found is the go.mod
// path that was read ("" if none), modPath its module path.
func judgeNestedModule(dir string, v version, found, modPath string) string {
	if found == "" {
		return fmt.Sprintf("no go.mod at %s/go.mod at the merge commit (nor at %s/v%d/go.mod): "+
			"%s/%s is a nested module's tag, and the Go Modules Reference (#vcs-version) wants the tag prefix to be the module subdirectory",
			dir, dir, v[0], dir, v)
	}
	base, major := pathMajor(modPath)
	switch {
	case major == 0 && v[0] >= 2:
		return fmt.Sprintf("%s declares module %q, which has no /vN major version suffix, but the tag's major version is %d: "+
			"a module path must end in /v%d for a v%d tag (go.dev/ref/mod#vcs-version)", found, modPath, v[0], v[0], v[0])
	case major != 0 && major != v[0]:
		return fmt.Sprintf("%s declares module %q (major version %d) but the tag's major version is %d: "+
			"the two must agree (go.dev/ref/mod#vcs-version)", found, modPath, major, v[0])
	}
	if !strings.HasSuffix(base, "/"+dir) {
		want := "/" + dir
		if major != 0 {
			want += fmt.Sprintf("/v%d", major)
		}
		return fmt.Sprintf("%s declares module %q, which does not end in %s: the tag prefix %q must be the module's subdirectory",
			found, modPath, want, dir+"/")
	}
	return ""
}

// readGoMod reads path at sha: its text, and whether the file exists.
func readGoMod(token, repo, path, sha string) (string, bool, error) {
	resp, err := api(token, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", apiBase, repo, path, url.QueryEscape(sha)), nil)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", false, nil
	default:
		return "", false, refused("reading "+path, resp)
	}
	var f struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&f); err != nil {
		return "", false, fmt.Errorf("reading %s: %w", path, err)
	}
	if f.Type != "file" || f.Encoding != "base64" {
		return "", false, nil // a directory or a symlink is not a go.mod
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return "", false, fmt.Errorf("reading %s: %w", path, err)
	}
	return string(b), true, nil
}

// checkNestedModule is asked at the merge commit. A non-empty refusal names what
// was found; err is a failure to ask.
func checkNestedModule(token, repo, tag, sha string) (refusal string, err error) {
	dir, ver, _ := splitTag(tag)
	v, perr := parseStrict(ver)
	if perr != nil {
		return perr.Error(), nil
	}
	cands := []string{dir + "/go.mod"}
	if v[0] >= 2 {
		cands = append(cands, fmt.Sprintf("%s/v%d/go.mod", dir, v[0]))
	}
	// A v2+ module may sit in <dir> or in <dir>/vN; the first candidate that
	// satisfies the rules wins, else the FIRST refusal is the one named.
	first := ""
	for _, p := range cands {
		text, ok, err := readGoMod(token, repo, p, sha)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		why := fmt.Sprintf("%s has no module directive", p)
		if mp, ok := moduleDirPath(text); ok {
			why = judgeNestedModule(dir, v, p, mp)
		}
		if why == "" {
			return "", nil
		}
		if first == "" {
			first = why
		}
	}
	if first != "" {
		return first, nil
	}
	return judgeNestedModule(dir, v, "", ""), nil
}
