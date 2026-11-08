package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/ghauth"
	"github.com/go-gitsafe/gitsafe/issuegate"
)

// issueSettle is how long to let GitHub apply its own closing keywords before
// the issues are read back. It closes them asynchronously, a moment after the
// merge; reading at once would find them open and close them a second time.
// A variable so a test need not wait.
var issueSettle = 3 * time.Second

// exitIssueNotClosed is the status when the pull request WAS merged but an
// issue it closes could not be closed. ghrelease reads it: the merge happened,
// so the tag must not be abandoned, but the run is not a success.
const exitIssueNotClosed = 3

// ghIssues is issuegate.Client over the REST API, through this command's own
// token handling, retry and refusal reading. Each Issue call is ONE GET.
type ghIssues struct{ token string }

func (g ghIssues) Issue(repo string, number int) (issuegate.Issue, error) {
	var out struct {
		State string          `json:"state"`
		PR    json.RawMessage `json:"pull_request"`
	}
	url := fmt.Sprintf("%s/repos/%s/issues/%d", apiBase, repo, number)
	var found bool
	err := retrying(func() error {
		var err error
		found, err = getOrMissing(g.token, url, &out)
		return err
	})
	if err != nil || !found {
		return issuegate.Issue{}, err
	}
	return issuegate.Issue{Exists: true, IsPR: len(out.PR) > 0 && string(out.PR) != "null", Open: out.State == "open"}, nil
}

func (g ghIssues) Comment(repo string, number int, text string) error {
	return comment(g.token, repo, number, text)
}

// Close closes the issue as completed (REST "Update an issue").
func (g ghIssues) Close(repo string, number int) error {
	body, _ := json.Marshal(map[string]string{"state": "closed", "state_reason": "completed"})
	return retrying(func() error {
		req, err := http.NewRequest(http.MethodPatch,
			fmt.Sprintf("%s/repos/%s/issues/%d", apiBase, repo, number), bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+g.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return fmt.Errorf("closing: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			return ghauth.Refused(resp.StatusCode, resp.Header, b, ghauth.Now(resp.Header, now()))
		}
		return nil
	})
}

// getOrMissing is getOnce, except that 404 is an answer ("there is no such
// issue") rather than an error: a `#12` in prose is often no issue at all.
func getOrMissing(token, url string, into any) (found bool, err error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return false, fmt.Errorf("asking GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, json.NewDecoder(resp.Body).Decode(into)
	case http.StatusNotFound, http.StatusGone:
		return false, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return false, ghauth.Refused(resp.StatusCode, resp.Header, body, ghauth.Now(resp.Header, now()))
}

// issuesAfterMerge closes what GitHub did not, says what it did, and returns
// the number of issues that could not be closed.
func issuesAfterMerge(c issuegate.Client, v issuegate.Verdict, repo string, number int, sha string, stdout, stderr io.Writer) int {
	if len(v.Closing) == 0 {
		return 0
	}
	sleep(issueSettle)
	failed := 0
	for _, r := range issuegate.AfterMerge(c, v, repo, number, sha) {
		switch {
		case r.Error != nil:
			failed++
			fmt.Fprintf(stderr, "ghmerge: %s#%d was merged, but %s could NOT be closed: %v\n", repo, number, r.Ref, r.Error)
		case r.ByUs:
			fmt.Fprintf(stdout, "issue %s was left open by GitHub; closed it (Closed by %s#%d)\n", r.Ref, repo, number)
		default:
			fmt.Fprintf(stdout, "issue %s closed by GitHub\n", r.Ref)
		}
	}
	return failed
}

// issueNames lists references for a message.
func issueNames(rs []issuegate.Ref) string {
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = r.String()
	}
	return strings.Join(s, ", ")
}
