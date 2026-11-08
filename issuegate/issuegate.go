// Package issuegate decides whether a pull request may be merged while the
// issues it mentions are still open, and what must be done to them afterwards.
//
// Bibliography, opened 2026-11-08:
//
//   - GitHub Docs, "Linking a pull request to an issue": the closing keywords
//     are close, closes, closed, fix, fixes, fixed, resolve, resolves,
//     resolved; the syntax is `KEYWORD #N` or `KEYWORD OWNER/REPO#N`, one
//     keyword per issue ("Resolves #10, resolves #123"); keywords are
//     interpreted only when the pull request targets the DEFAULT branch.
//   - GitHub REST, "Get an issue": GET /repos/{owner}/{repo}/issues/{n}. It
//     answers for pull requests too, marking them with a `pull_request` key,
//     which is how this package tells the two apart with ONE call.
//
// ⛔ WHY A GATE AND NOT A REMINDER. A pull request that says "see #12" leaves
// #12 open when it lands, and nobody is told: the issue stays on the list as if
// the work were not done. The rule is that an issue is closed as the pull
// request lands, or is left open ON PURPOSE and the body says so (`Refs #12`).
// A bare mention is neither, so it is refused before anything is written.
package issuegate

import (
	"fmt"
	"regexp"
	"strings"
)

// Kind is what a pull request body says it does to an issue.
type Kind int

const (
	// Bare is a mention with no keyword in front of it: neither "closes" nor
	// "stays open on purpose". It is the case the gate refuses.
	Bare Kind = iota
	// Refs says the issue stays open on purpose.
	Refs
	// Closes says the issue is closed by this pull request.
	Closes
)

func (k Kind) String() string {
	switch k {
	case Closes:
		return "closes"
	case Refs:
		return "refs"
	}
	return "bare"
}

// Ref is one issue reference found in a body.
type Ref struct {
	Repo   string // owner/repo, the pull request's own when the body wrote `#N`
	Number int
	Kind   Kind
}

// String is the reference as it is written: owner/repo#N.
func (r Ref) String() string { return fmt.Sprintf("%s#%d", r.Repo, r.Number) }

// MaxRefs bounds how many distinct references a body may carry. One GET is
// spent on each, from a budget shared with the whole fleet, and a body is
// untrusted text: a thousand `#N` must not become a thousand requests.
const MaxRefs = 50

var (
	fence       = regexp.MustCompile("(?s)(```|~~~).*?(```|~~~|\\z)")
	htmlComment = regexp.MustCompile(`(?s)<!--.*?(-->|\z)`)
	codeSpan    = regexp.MustCompile("`[^`\n]*`")
	// An issue URL is a reference with a long spelling; GitHub honours it after
	// a keyword. Any other URL (a pull request, a commit, a page) is dropped,
	// because `#12` inside it is an anchor or a PR number, not an issue.
	issueURL = regexp.MustCompile(`https?://github\.com/([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+)/issues/(\d+)\b`)
	anyURL   = regexp.MustCompile(`(?i)\b(?:https?|ftp)://\S+`)
	ref      = regexp.MustCompile(`(?:^|[^\w/#&.-])((?:[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+)?)#(\d{1,9})\b`)
	keyword  = regexp.MustCompile(`(?i)(?:^|[^A-Za-z])(clos(?:e[sd]?)|fix(?:es|ed)?|resolv(?:e[sd]?)|refs?)[:\s]*$`)
	joiner   = regexp.MustCompile(`(?i)^\s*(?:,\s*(?:and\s+)?|and\s+|&\s*)$`)
)

// Strip removes what is not prose: fenced blocks, HTML comments (a pull request
// template's instructions live there), inline code spans, and URLs. An issue URL
// is kept in its short spelling.
func Strip(body string) string {
	body = fence.ReplaceAllString(body, "\n")
	body = htmlComment.ReplaceAllString(body, " ")
	body = codeSpan.ReplaceAllString(body, " ")
	body = issueURL.ReplaceAllString(body, "$1#$2")
	// Keep the short spelling the line above produced: only URLs are removed.
	return anyURL.ReplaceAllString(body, " ")
}

// Parse returns the distinct issue references of body, in order of first
// appearance, each with the strongest thing the body says about it. Repo is the
// pull request's own repository, for a bare `#N`.
//
// Rules, from GitHub's: a closing keyword applies to the ONE reference right
// after it ("Closes #1, #2" closes only #1, and #2 is Bare). `Refs` is more
// generous because it is safe: "Refs #1, #2 and #3" keeps all three open. A
// keyword on another line does not count.
//
// A reference seen twice is Closes if any occurrence closes, else Refs if any
// says Refs, else Bare. Whether a number is an issue at all is not known here:
// that is a GET (see Classify).
func Parse(body, repo string) []Ref {
	var out []Ref
	index := map[string]int{}
	for _, line := range strings.Split(Strip(body), "\n") {
		prev := -1 // end offset of the previous reference on this line
		prevKind := Bare
		for _, m := range ref.FindAllStringSubmatchIndex(line, -1) {
			start := m[2] // start of the owner/repo#N text, after the lead char
			end := m[1]
			r := repo
			if m[3] > m[2] {
				r = line[m[2]:m[3]]
			}
			n := 0
			fmt.Sscanf(line[m[4]:m[5]], "%d", &n)
			if n <= 0 {
				continue
			}
			kind := Bare
			if km := keyword.FindStringSubmatch(line[:start]); km != nil {
				kind = Refs
				if !strings.HasPrefix(strings.ToLower(km[1]), "ref") {
					kind = Closes
				}
			} else if prev >= 0 && prevKind == Refs && joiner.MatchString(line[prev:start]) {
				kind = Refs
			}
			prev, prevKind = end, kind
			key := strings.ToLower(r) + "#" + fmt.Sprint(n)
			if i, ok := index[key]; ok {
				if kind > out[i].Kind {
					out[i].Kind = kind
				}
				continue
			}
			index[key] = len(out)
			out = append(out, Ref{Repo: r, Number: n, Kind: kind})
		}
	}
	return out
}

// Issue is what GET /repos/{r}/issues/{n} says that this gate needs.
type Issue struct {
	Exists bool // false: GitHub answered 404, so the number is not an issue
	IsPR   bool // the `pull_request` key was present
	Open   bool
}

// Client is the part of GitHub the gate talks to. The tests give it a fake.
type Client interface {
	// Issue is ONE GET. A 404 is Issue{Exists:false}, not an error; anything
	// else that is not an answer is an error, and the gate then refuses.
	Issue(repo string, number int) (Issue, error)
	// Comment writes a comment on an issue or pull request.
	Comment(repo string, number int, text string) error
	// Close closes an issue as completed.
	Close(repo string, number int) error
}

// Verdict is the gate's reading of a body, before the merge.
type Verdict struct {
	Closing   []Ref // open issues the body closes
	Refs      []Ref // open issues that stay open on purpose
	Unclassed []Ref // open issues mentioned bare: the reason to refuse
	Ignored   []Ref // pull requests, closed issues, numbers that are no issue
}

// Classify parses body and resolves every reference with one GET each. It
// writes nothing. A body with more than MaxRefs distinct references, or a GET
// that fails, is an error: the gate fails closed.
func Classify(c Client, body, repo string) (Verdict, error) {
	var v Verdict
	refs := Parse(body, repo)
	if len(refs) > MaxRefs {
		return v, fmt.Errorf("the pull request body references %d distinct issues; the gate reads at most %d", len(refs), MaxRefs)
	}
	for _, r := range refs {
		is, err := c.Issue(r.Repo, r.Number)
		if err != nil {
			return v, fmt.Errorf("reading %s: %w", r, err)
		}
		if !is.Exists || is.IsPR || !is.Open {
			v.Ignored = append(v.Ignored, r)
			continue
		}
		switch r.Kind {
		case Closes:
			v.Closing = append(v.Closing, r)
		case Refs:
			v.Refs = append(v.Refs, r)
		default:
			v.Unclassed = append(v.Unclassed, r)
		}
	}
	return v, nil
}

// Refusal is the message for a verdict with unclassified issues, or "" when the
// merge may go ahead.
func (v Verdict) Refusal() string {
	if len(v.Unclassed) == 0 {
		return ""
	}
	names := make([]string, len(v.Unclassed))
	for i, r := range v.Unclassed {
		names[i] = r.String()
	}
	return fmt.Sprintf("the pull request body mentions open issue(s) %s without saying what happens to them. "+
		"Write `Closes %s` (or Fixes/Resolves) if this pull request finishes it, or `Refs %s` if it stays open on purpose; "+
		"or merge anyway with --issues-waiver \"<reason>\", which is recorded as a comment",
		strings.Join(names, ", "), v.Unclassed[0], v.Unclassed[0])
}

// Closed is the outcome for one issue the body closes, after the merge.
type Closed struct {
	Ref   Ref
	ByUs  bool  // GitHub had not closed it and this did
	Error error // set when it could not be closed
}

// AfterMerge re-reads each issue the body closes (one GET each) and closes, with
// a comment, any that GitHub left open: a cross-repository reference, a pull
// request that did not target the default branch, or a missing permission.
func AfterMerge(c Client, v Verdict, prRepo string, prNumber int, mergeSHA string) []Closed {
	sha := mergeSHA
	if len(sha) > 12 {
		sha = sha[:12]
	}
	var out []Closed
	for _, r := range v.Closing {
		res := Closed{Ref: r}
		is, err := c.Issue(r.Repo, r.Number)
		switch {
		case err != nil:
			res.Error = fmt.Errorf("re-reading: %w", err)
		case !is.Open:
			// GitHub did it.
		default:
			res.ByUs = true
			text := fmt.Sprintf("Closed by %s#%d (merge %s)", prRepo, prNumber, sha)
			if err := c.Comment(r.Repo, r.Number, text); err != nil {
				res.Error = fmt.Errorf("commenting: %w", err)
			} else if err := c.Close(r.Repo, r.Number); err != nil {
				res.Error = fmt.Errorf("closing: %w", err)
			}
		}
		out = append(out, res)
	}
	return out
}

// WaiverStamp is the comment a waived gate leaves on the pull request.
func WaiverStamp(reason string) string { return "issues gate waived: " + reason }
