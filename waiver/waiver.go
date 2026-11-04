// Package waiver is the one place that decides whether a red check may be
// waived because it NEVER STARTED, shared by ghmerge and ghrelease so there is
// one definition of the rule.
//
// # Why it exists
//
// GitHub's hosted macOS arm64 runners can have no capacity: a job is queued
// for about 15 minutes, then ends "cancelled" or "failure" with zero steps run
// and the annotation "The job was not acquired by Runner of type hosted even
// after multiple attempts". Nothing about the code was judged. ghmerge and
// ghrelease refuse every non-green check, so a person who has run the tests
// locally on the same platform had no honest way through.
//
// # The rule (all four, or refuse)
//
//   - the named check exists on the commit being judged;
//   - its job never started: no runner was assigned AND no step ran
//     (NeverStarted). A check that ran and failed is NEVER waivable;
//   - every other check is green (the caller's own gate, run without it);
//   - the evidence is a comment on that pull request, written by the
//     authenticated user, that contains the full 40-hex SHA of the commit being
//     judged (CheckEvidence).
//
// A waiver is never silent: the caller posts Stamp on the pull request before
// it writes anything else.
//
// # What "never started" reads
//
// GitHub REST, "Get a job for a workflow run" (GET
// /repos/{owner}/{repo}/actions/jobs/{job_id}), opened 2026-11-04: runner_id is
// "integer or null" and runner_name "string or null" (null until a runner is
// assigned); steps[] carries each step's status, conclusion and started_at. For
// an Actions job the check run's id IS the job id. "List jobs for a workflow
// run" (GET /repos/{owner}/{repo}/actions/runs/{run_id}/jobs) returns the same
// objects, which is what ghrelease reads for the merge commit.
//
// The check-run annotation text is deliberately NOT the test: text is for
// people and can be reworded; a runner that was never assigned and a step list
// that never began are structure.
package waiver

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Getter performs an authenticated GET of an absolute URL and decodes the JSON
// answer into into. Each command passes its own, so its retry, budget and test
// seams apply.
type Getter func(url string, into any) error

// Job is the part of a workflow job the rule reads.
type Job struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion string  `json:"conclusion"`
	RunnerID   *int64  `json:"runner_id"`
	RunnerName *string `json:"runner_name"`
	Steps      []Step  `json:"steps"`
}

// Step is one step of a job.
type Step struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	StartedAt  string `json:"started_at"`
}

// NeverStarted says whether the job may be waived on the strength of its own
// record, and when not, why (a sentence naming the first condition that failed).
func NeverStarted(j Job) (bool, string) {
	if j.Status != "completed" {
		return false, fmt.Sprintf("it is %s, not finished: wait for it to end", j.Status)
	}
	switch j.Conclusion {
	case "failure", "cancelled", "timed_out":
	default:
		return false, fmt.Sprintf("it concluded %q, so there is nothing to waive", j.Conclusion)
	}
	if j.RunnerID != nil && *j.RunnerID != 0 {
		return false, fmt.Sprintf("a runner was acquired (runner_id %d): the job started, and a check that ran can never be waived", *j.RunnerID)
	}
	if j.RunnerName != nil && *j.RunnerName != "" {
		return false, fmt.Sprintf("a runner was acquired (%s): the job started, and a check that ran can never be waived", *j.RunnerName)
	}
	for _, s := range j.Steps {
		if s.StartedAt != "" || s.Conclusion == "success" || s.Conclusion == "failure" {
			return false, fmt.Sprintf("step %q started: a check that ran can never be waived", s.Name)
		}
	}
	return true, ""
}

// FetchJob reads one job (for a check run, its id).
func FetchJob(get Getter, api, repo string, id int64) (Job, error) {
	var j Job
	err := get(fmt.Sprintf("%s/repos/%s/actions/jobs/%d", api, repo, id), &j)
	return j, err
}

// RunJobs reads the jobs of one workflow run.
func RunJobs(get Getter, api, repo string, run int64) ([]Job, error) {
	var out struct {
		Total int   `json:"total_count"`
		Jobs  []Job `json:"jobs"`
	}
	if err := get(fmt.Sprintf("%s/repos/%s/actions/runs/%d/jobs?per_page=100", api, repo, run), &out); err != nil {
		return nil, err
	}
	if out.Total > len(out.Jobs) {
		return nil, fmt.Errorf("run %d has %d jobs and only %d were read", run, out.Total, len(out.Jobs))
	}
	return out.Jobs, nil
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// IsFullSHA reports whether s is a 40-hex lower-case commit id.
func IsFullSHA(s string) bool { return fullSHA.MatchString(s) }

// ParseEvidence reads a PR comment URL
// (https://github.com/{repo}/pull/{n}#issuecomment-{id}) and returns the comment
// id. It refuses anything that is not a comment of THIS pull request.
func ParseEvidence(raw, repo string, number int) (int64, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") {
		return 0, fmt.Errorf("evidence %q is not an https://github.com/%s/pull/%d#issuecomment-<id> URL", raw, repo, number)
	}
	want := fmt.Sprintf("/%s/pull/%d", repo, number)
	if !strings.EqualFold(u.Path, want) && !strings.EqualFold(u.Path, fmt.Sprintf("/%s/issues/%d", repo, number)) {
		return 0, fmt.Errorf("evidence %q is not a comment on %s#%d", raw, repo, number)
	}
	id, ok := strings.CutPrefix(u.Fragment, "issuecomment-")
	n, err := strconv.ParseInt(id, 10, 64)
	if !ok || err != nil || n <= 0 {
		return 0, fmt.Errorf("evidence %q names no comment (want the #issuecomment-<id> link)", raw)
	}
	return n, nil
}

// CheckEvidence refuses unless the comment exists on the pull request, was
// written by the authenticated user and contains the full sha.
func CheckEvidence(get Getter, api, repo string, number int, id int64, sha string) error {
	if !IsFullSHA(sha) {
		return fmt.Errorf("%q is not a full 40-hex commit SHA, so no evidence can name it", sha)
	}
	var me struct {
		Login string `json:"login"`
	}
	if err := get(api+"/user", &me); err != nil {
		return fmt.Errorf("reading who the token belongs to: %w", err)
	}
	if me.Login == "" {
		return errors.New("GitHub did not say who the token belongs to")
	}
	var c struct {
		Body     string `json:"body"`
		IssueURL string `json:"issue_url"`
		User     struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := get(fmt.Sprintf("%s/repos/%s/issues/comments/%d", api, repo, id), &c); err != nil {
		return fmt.Errorf("reading the evidence comment: %w", err)
	}
	if !strings.HasSuffix(c.IssueURL, fmt.Sprintf("/issues/%d", number)) {
		return fmt.Errorf("the evidence comment %d is not on %s#%d", id, repo, number)
	}
	if !strings.EqualFold(c.User.Login, me.Login) {
		return fmt.Errorf("the evidence comment was written by %q, not by the authenticated user %q", c.User.Login, me.Login)
	}
	if !strings.Contains(strings.ToLower(c.Body), sha) {
		return fmt.Errorf("the evidence comment does not contain the full SHA %s of the commit being judged "+
			"(a run at another commit proves nothing about this one)", sha)
	}
	return nil
}

// Names reports the waived names in the order given, refusing an empty or
// duplicated one.
func Names(waive []string) error {
	seen := map[string]bool{}
	for _, w := range waive {
		if strings.TrimSpace(w) == "" {
			return errors.New("--waive needs a check name")
		}
		if seen[w] {
			return fmt.Errorf("--waive %q is given twice", w)
		}
		seen[w] = true
	}
	return nil
}

// Stamp is the comment that makes a waiver visible.
func Stamp(verb string, checks []string, evidence, sha string) string {
	q := make([]string, len(checks))
	for i, c := range checks {
		q[i] = "'" + c + "'"
	}
	return fmt.Sprintf("%s with %s waived: no runner acquired; evidence %s (local run at %s)",
		verb, strings.Join(q, ", "), evidence, sha)
}
