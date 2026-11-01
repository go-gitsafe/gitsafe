package main

// Waiting for CI on the default branch before tagging.
//
// A release tag marks a merged, CI-GREEN default-branch commit. Until v0.31.0
// ghrelease tagged the merge commit as soon as the merge succeeded, which only
// proves the PULL REQUEST's checks were green. On 2026-11-01 go-llmflow/gateway
// main turned red on a merge commit whose PR checks had been green (a random
// fuzz smoke job found a panic, run 41286754677), and
// `ghrelease go-llmflow/gateway 126 v0.24.1` tagged 9454b864 while that
// commit's own main run (41295856213) was still queued.
//
// Sources, opened before this was written:
//
//   - GitHub REST, "List workflow runs for a repository",
//     GET /repos/{owner}/{repo}/actions/runs, https://docs.github.com/en/rest/actions/workflow-runs#list-workflow-runs-for-a-repository
//     filters: branch, head_sha, per_page (max 100); fields: total_count,
//     workflow_runs[].{id,name,status,conclusion,head_sha,html_url}. The page
//     types conclusion as "string or null" and lists no values, so only the
//     ones the run object is known to carry are named below and EVERYTHING ELSE
//     counts as a failure: an unknown verdict must not tag.
//   - GitHub REST, "Get a workflow run", GET /repos/{owner}/{repo}/actions/runs/{run_id}:
//     the same object; the list already returns it whole, so one list call per
//     poll answers for every run, instead of one call per run.
//
// The polling reuses the budget of backfill.go: every answer's
// X-RateLimit-Remaining is noted and the wait stops below the margin; a refused
// answer (403 secondary limit included) goes through refused()/ghauth.WhyRefused
// and ends the wait with its reason. There is no retry loop around a refusal.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultWaitMain = 30 * time.Minute
	runsPerPage     = 100
)

// Seams, so the tests run a half-hour wait in no time (and plant the old
// behaviour, tagging without waiting, as a control).
var (
	waitMain    = waitMainCI
	sleep       = time.Sleep
	mainGrace   = 2 * time.Minute  // no run appears within this: there is no CI on push to main
	pollFirst   = 10 * time.Second // the first interval; it grows by half each poll...
	pollCeiling = time.Minute      // ...up to this
)

// workflowRun is the part of a workflow run the verdict needs.
type workflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"head_sha"`
	HTMLURL    string `json:"html_url"`
}

// listRuns returns the workflow runs of repo whose head_sha is sha, on branch
// when it is known. total is GitHub's count, which exceeds len(runs) when the
// first page did not hold them all.
func listRuns(token, repo, sha, branch string, b *budget) (runs []workflowRun, total int, err error) {
	q := url.Values{"head_sha": {sha}, "per_page": {fmt.Sprint(runsPerPage)}}
	if branch != "" {
		q.Set("branch", branch)
	}
	resp, err := api(token, http.MethodGet, fmt.Sprintf("%s/repos/%s/actions/runs?%s", apiBase, repo, q.Encode()), nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b.noteREST(resp.Header)
	if resp.StatusCode != http.StatusOK {
		return nil, 0, refused("listing the workflow runs of the merge commit", resp)
	}
	var out struct {
		TotalCount   int           `json:"total_count"`
		WorkflowRuns []workflowRun `json:"workflow_runs"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("reading the workflow runs: %w", err)
	}
	// head_sha is already a filter; checking it again costs nothing and keeps a
	// server that ignores the filter from lending us another commit's verdict.
	for _, r := range out.WorkflowRuns {
		if r.HeadSHA == sha {
			runs = append(runs, r)
		}
	}
	return runs, out.TotalCount, nil
}

// mainVerdict classifies the runs: still pending, failed, or all well.
// skipped and neutral are not failures; anything else that is not success is.
func mainVerdict(runs []workflowRun) (pending, failed, soft []workflowRun) {
	for _, r := range runs {
		switch {
		case r.Status != "completed":
			pending = append(pending, r)
		case r.Conclusion == "success":
		case r.Conclusion == "skipped" || r.Conclusion == "neutral":
			soft = append(soft, r)
		default:
			failed = append(failed, r)
		}
	}
	return
}

func runList(rs []workflowRun, withConclusion bool) string {
	var sb strings.Builder
	for _, r := range rs {
		c := r.Status
		if withConclusion {
			c = r.Conclusion
			if c == "" {
				c = "no conclusion"
			}
		}
		fmt.Fprintf(&sb, "  %s: %s %s\n", r.Name, c, r.HTMLURL)
	}
	return sb.String()
}

// waitMainCI blocks until every workflow run of the merge commit on the default
// branch is completed, within wait, and returns 0 only when the commit may be
// tagged. It prints why on stderr otherwise.
//
// wait 0 disables the wait, loudly. noMainCI makes "no run appeared" an
// explicit, accepted choice; without it, that is a refusal.
func waitMainCI(token, repo, sha, branch string, wait time.Duration, noMainCI bool, b *budget, stdout, stderr io.Writer) int {
	if wait == 0 {
		fmt.Fprintf(stderr, "ghrelease: WARNING: -wait-main 0: NOT waiting for CI on %s at %s. "+
			"The tag may mark a commit whose main run is unfinished or RED.\n", repo, short(sha))
		return 0
	}
	start := now()
	deadline := start.Add(wait)
	grace := min(mainGrace, wait)
	interval := pollFirst
	fmt.Fprintf(stdout, "waiting up to %s for CI on the default branch at %s\n", wait, short(sha))
	for {
		if why := b.low(); why != "" {
			fmt.Fprintf(stderr, "ghrelease: not tagging: %s, so the CI of %s on main cannot be watched further; the pull request IS merged\n", why, short(sha))
			return 1
		}
		runs, total, err := listRuns(token, repo, sha, branch, b)
		if err != nil {
			fmt.Fprintf(stderr, "ghrelease: not tagging: %v; the pull request IS merged, the CI of %s on main is unknown\n", err, short(sha))
			return 1
		}
		if total > len(runs) {
			fmt.Fprintf(stderr, "ghrelease: not tagging: %s has %d workflow runs and only %d were read; "+
				"that is too many to judge here -- check them and tag by hand\n", short(sha), total, len(runs))
			return 1
		}
		pending, failed, soft := mainVerdict(runs)
		switch {
		case len(failed) > 0:
			fmt.Fprintf(stderr, "ghrelease: NOT tagging: CI on main failed at %s (the pull request IS merged, and main is not green):\n%s",
				short(sha), runList(failed, true))
			return 1
		case len(runs) == 0 && now().Sub(start) >= grace:
			if noMainCI {
				fmt.Fprintf(stderr, "ghrelease: no workflow run appeared on %s at %s within %s; -no-main-ci was given, so tagging without CI on main\n",
					repo, short(sha), grace)
				return 0
			}
			fmt.Fprintf(stderr, "ghrelease: NOT tagging: no workflow run appeared on the default branch at %s within %s "+
				"(no CI on push to main?). A repository without main CI is an explicit choice: pass -no-main-ci\n", short(sha), grace)
			return 1
		case len(runs) > 0 && len(pending) == 0:
			if len(soft) > 0 {
				fmt.Fprintf(stdout, "%d run(s) ended skipped or neutral, which are not failures:\n%s", len(soft), runList(soft, true))
			}
			fmt.Fprintf(stdout, "CI on main is green at %s (%d run(s))\n", short(sha), len(runs))
			return 0
		case !now().Before(deadline):
			fmt.Fprintf(stderr, "ghrelease: NOT tagging: CI on main at %s had not finished after %s (the pull request IS merged):\n%s",
				short(sha), wait, pendingOrNone(pending))
			return 1
		}
		d := min(interval, deadline.Sub(now()))
		sleep(d)
		interval = min(interval*3/2, pollCeiling)
	}
}

func pendingOrNone(p []workflowRun) string {
	if len(p) == 0 {
		return "  (no run had appeared)\n"
	}
	return runList(p, false)
}
