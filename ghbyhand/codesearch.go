package ghbyhand

import "strings"

// ⛔⛔ GITHUB CODE SEARCH IS AN INDEX, AND ITS ZERO IS AN ANSWER NOT OBTAINED.
//
// Measured 2026-11-24 (memory unread-answer-reads-as-a-negative, instance 7):
//
//	gh api search/code -f q="A11yElements repo:claimward/claimward-vpn-app-linux"
//	  → total_count 0, read as "the symbol is absent"
//
// while the symbol was on main at view.go:889. Code search answers about an
// INDEX that lags, covers only part of a repository and promises no coverage,
// so "0 results" cannot tell "absent" from "not indexed (yet)". The repository
// itself can answer, with the coverage it has: `git grep` over a fetched ref.
//
// ⛔ THIS IS A READ, AND IT IS REFUSED ANYWAY. The rest of this package refuses
// writes; this one refuses the one read whose empty answer is indistinguishable
// from a negative. Every other search (`gh search issues`, `gh search prs`,
// `gh api search/issues`) stays allowed: those answer from the tracker, which is
// the source of truth, not from a lagging index.
//
// NO ESCAPE, like the rest of this package: the replacement is the same command
// shape with a real answer, so a prefix would buy nothing but the defect.

// CodeSearchInstead is what the refusal tells the caller to run.
const CodeSearchInstead = "`git fetch` then `git grep -n <pattern> origin/main` in a clone " +
	"under ~/Documents/VCS/GIT/localhost (go-<org>-<repo>, found with `ls`). If no clone " +
	"exists, make a shallow one there first: `git clone --depth 1 " +
	"https://github.com/<owner>/<repo> ~/Documents/VCS/GIT/localhost/<owner>-<repo>`. " +
	"`git grep` over a fetched ref reads every file on the branch, so an empty answer " +
	"means absent; GitHub's code-search index can lag or skip files, so its zero is " +
	"an answer not obtained"

// codeSearchAct is the act, in words, for [Finding.Why].
const codeSearchAct = "ask GitHub's code-search index, whose zero is an answer not obtained"

// codeSearchFinding builds the refusal.
func codeSearchFinding(st string) Finding {
	return Finding{Match: short(st), Act: codeSearchAct, Instead: CodeSearchInstead, CodeSearch: true}
}

// codeSearchGH reports whether `gh <words[1:]>` is `gh search code`. words has
// already lost a global -R/--repo and starts at `gh`.
func codeSearchGH(words []string) bool {
	return len(words) >= 3 && words[1] == "search" && words[2] == "code"
}

// apiValueFlags are the `gh api` flags that take the NEXT word as their value,
// so that word is not the endpoint even when it contains a slash
// (`-f q=repo:o/r`).
var apiValueFlags = map[string]bool{
	"-X": true, "--method": true, "-f": true, "--raw-field": true,
	"-F": true, "--field": true, "-H": true, "--header": true,
	"-q": true, "--jq": true, "-t": true, "--template": true,
	"--input": true, "--hostname": true, "--cache": true, "-p": true, "--preview": true,
}

// endOfWord returns the index of the last field of the shell word that starts at
// words[i]. A quoted value with spaces (`-H "Accept: application/json"`) arrives
// split on them, and a slash in its tail must not be taken for the endpoint.
func endOfWord(words []string, i int) int {
	if i >= len(words) {
		return len(words) - 1
	}
	w := words[i]
	if w == "" || (w[0] != '"' && w[0] != '\'') {
		return i
	}
	q := w[:1]
	if len(w) > 1 && strings.HasSuffix(w, q) {
		return i
	}
	for j := i + 1; j < len(words); j++ {
		if strings.HasSuffix(words[j], q) {
			return j
		}
	}
	return len(words) - 1
}

// apiEndpoint returns the first positional word of `gh api`, quotes trimmed,
// skipping flags and the values they take. words starts at `gh`.
func apiEndpoint(words []string) string {
	for i := 2; i < len(words); i++ {
		w := strings.Trim(words[i], `"'`)
		switch {
		case apiValueFlags[w]:
			i = endOfWord(words, i+1)
		case strings.HasPrefix(w, "-"):
			// A flag with its value attached (`-fq=…`, `--method=GET`) or a switch.
		default:
			return w
		}
	}
	return ""
}

// isCodeSearchPath reports whether an endpoint is GitHub's code search,
// whatever the spelling: a leading slash, a full https://api.github.com URL, a
// query string in the URL.
func isCodeSearchPath(p string) bool {
	p = strings.Trim(p, `"'`)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if i := strings.Index(strings.ToLower(p), "api.github.com"); i >= 0 {
		p = p[i+len("api.github.com"):]
	}
	p = strings.ToLower(strings.Trim(p, "/"))
	return p == "search/code"
}

// codeSearchFetcher reports whether words invoke curl or wget at the code-search
// endpoint of api.github.com, in any word (the URL, `--url …`, `--url=…`).
func codeSearchFetcher(words []string) bool {
	if len(words) == 0 {
		return false
	}
	c := strings.Trim(words[0], `"'`)
	if i := strings.LastIndexByte(c, '/'); i >= 0 {
		c = c[i+1:]
	}
	if c != "curl" && c != "wget" {
		return false
	}
	for _, w := range words[1:] {
		w = strings.Trim(w, `"'`)
		if i := strings.IndexByte(w, '='); i >= 0 && strings.HasPrefix(w, "--") {
			w = w[i+1:]
		}
		l := strings.ToLower(w)
		if i := strings.Index(l, "api.github.com"); i >= 0 && isCodeSearchPath(w) {
			return true
		}
	}
	return false
}
