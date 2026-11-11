package main

// Pull requests stacked on the head branch of the one just merged.
//
// GitHub does not retarget an open pull request whose BASE is a branch that gets
// deleted: it CLOSES it, and the review history and CI record go with it. Twice
// in four days merging a pull request here deleted the branch a second one was
// stacked on (go-avkit/avkit#100 -> #102, go-avkit/decode#28 -> #31), and each
// time a new pull request had to be opened by hand.
//
// So before the head branch is deleted, every OPEN pull request of the same
// repository whose base is that branch is moved onto the merged pull request's
// base, and each move is verified by reading the pull request back. Only when
// all of them are verified is the branch deleted. A move that fails keeps the
// branch: the stacked pull request still has its base.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-gitsafe/gitsafe/ghauth"
)

// exitRetargetFailed is the status when the pull request WAS merged but a pull
// request stacked on its head branch could not be retargeted, so the branch was
// kept. Distinct from exitIssueNotClosed (3).
const exitRetargetFailed = 4

// stackedOn lists the open pull requests of repo whose base is branch, every
// page of them.
func stackedOn(token, repo, branch string) ([]int, error) {
	const perPage = 100
	var all []int
	for page := 1; ; page++ {
		var got []struct {
			Number int `json:"number"`
		}
		u := fmt.Sprintf("%s/repos/%s/pulls?state=open&base=%s&per_page=%d&page=%d",
			apiBase, repo, url.QueryEscape(branch), perPage, page)
		if err := get(token, u, &got); err != nil {
			return nil, err
		}
		for _, g := range got {
			all = append(all, g.Number)
		}
		if len(got) < perPage {
			return all, nil
		}
	}
}

// setBase moves pull request number onto base (REST "Update a pull request").
func setBase(token, repo string, number int, base string) error {
	body, _ := json.Marshal(map[string]string{"base": base})
	return retrying(func() error {
		req, err := http.NewRequest(http.MethodPatch,
			fmt.Sprintf("%s/repos/%s/pulls/%d", apiBase, repo, number), bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return fmt.Errorf("retargeting: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			return ghauth.Refused(resp.StatusCode, resp.Header, b, ghauth.Now(resp.Header, now()))
		}
		return nil
	})
}

// retarget moves one pull request onto base and READS IT BACK: a 200 from the
// PATCH is not the base being changed.
func retarget(token, repo string, number int, base string) error {
	if err := setBase(token, repo, number, base); err != nil {
		return err
	}
	p, err := pullRequest(token, repo, number)
	if err != nil {
		return fmt.Errorf("reading it back: %w", err)
	}
	if p.State != "open" {
		return fmt.Errorf("it is %s after the retarget", p.State)
	}
	if p.Base.Ref != base {
		return fmt.Errorf("GitHub accepted the change but its base is still %q, not %q", p.Base.Ref, base)
	}
	return nil
}

// retargetStacked moves every open pull request stacked on branch onto base.
// It returns true when the branch may now be deleted: nothing was stacked on
// it, or everything stacked on it was moved and verified. It says what it did on
// stdout, and what it did not on stderr, naming the pull request.
func retargetStacked(token, repo, branch, base string, stdout, stderr io.Writer) bool {
	nums, err := stackedOn(token, repo, branch)
	if err != nil {
		fmt.Fprintf(stderr, "ghmerge: the pull request IS merged, but the branch %s was KEPT: "+
			"could not list the pull requests stacked on it: %v\n", branch, err)
		return false
	}
	ok := true
	for _, n := range nums {
		if base == "" {
			fmt.Fprintf(stderr, "ghmerge: the pull request IS merged, but %s#%d is stacked on %s and "+
				"its new base is unknown; branch %s KEPT\n", repo, n, branch, branch)
			ok = false
			continue
		}
		if err := retarget(token, repo, n, base); err != nil {
			fmt.Fprintf(stderr, "ghmerge: the pull request IS merged, but %s#%d, stacked on %s, "+
				"could not be retargeted to %s: %v\n", repo, n, branch, base, err)
			ok = false
			continue
		}
		fmt.Fprintf(stdout, "%s#%d was stacked on %s: retargeted to %s (verified)\n", repo, n, branch, base)
	}
	if !ok {
		fmt.Fprintf(stderr, "ghmerge: branch %s KEPT, because deleting it would close the pull "+
			"request(s) above; fix them, then delete it by hand\n", branch)
	}
	return ok
}
