package main

// Waiving a check that never started, on the default branch.
//
// ghrelease hands --waive/--evidence to ghmerge for the pull request's own
// checks (the rule lives in package waiver). The wait on the default branch
// judges the MERGE COMMIT, a different commit, so the same rule applies to it
// again and the evidence must name THAT commit's SHA: a local run at the merge
// commit, recorded as a comment on the pull request and passed as
// --main-evidence. Without it ghrelease merges, refuses to tag, and says what
// is missing.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-gitsafe/gitsafe/waiver"
)

// mainWaiver is the waiver in force for the default-branch wait. nil: none.
type mainWaiver struct {
	names    []string
	evidence string // --main-evidence; "" until the person has run the merge commit
	number   int
	tag      string
	stamped  string // the set already stamped, so a later poll does not repeat it
}

// getJSON is waiver.Getter over this command's api seam.
func getJSON(token string) waiver.Getter {
	return func(url string, into any) error {
		resp, err := api(token, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return refused("asking GitHub", resp)
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into)
	}
}

// accept judges the failed workflow runs of the merge commit. It returns the
// names it waived and "" when every failure is a job that never started and was
// named; otherwise why not. It writes nothing.
//
// Conditions: every non-green job of every failed run is named in --waive (c)
// and never started (b); a failed run with no job to name cannot be waived; the
// evidence (d) exists, is the user's, and holds the merge commit's full SHA.
func (w *mainWaiver) accept(token, repo, sha string, failed []workflowRun) (used []string, why string) {
	get := getJSON(token)
	named := map[string]bool{}
	for _, n := range w.names {
		named[n] = true
	}
	usedSet := map[string]bool{}
	for _, r := range failed {
		jobs, err := waiver.RunJobs(get, apiBase, repo, r.ID)
		if err != nil {
			return nil, fmt.Sprintf("reading the jobs of run %q: %v", r.Name, err)
		}
		bad := 0
		for _, j := range jobs {
			if j.Status == "completed" && (j.Conclusion == "success" || j.Conclusion == "skipped" || j.Conclusion == "neutral") {
				continue
			}
			bad++
			if !named[j.Name] {
				return nil, fmt.Sprintf("job %q of run %q is %s and is not waived (condition c: every other check must be green)", j.Name, r.Name, orNone(j.Conclusion, j.Status))
			}
			if ok, reason := waiver.NeverStarted(j); !ok {
				return nil, fmt.Sprintf("--waive %q on the merge commit: %s (condition b)", j.Name, reason)
			}
			usedSet[j.Name] = true
		}
		if bad == 0 {
			return nil, fmt.Sprintf("run %q failed but none of its jobs is red, so there is no check to waive", r.Name)
		}
	}
	for _, n := range w.names {
		if usedSet[n] {
			used = append(used, n)
		}
	}
	if w.evidence == "" {
		return nil, fmt.Sprintf("every failure on %s is a never-started job (%s) and could be waived, but the evidence for the MERGE COMMIT is missing.\n"+
			"ghrelease:   1. run the tests locally at the merge commit %s;\n"+
			"ghrelease:   2. comment the result, with that full SHA, on %s#%d, as the authenticated user;\n"+
			"ghrelease:   3. run the same command again with --main-evidence <that comment's URL>\n"+
			"ghrelease:   (the pull request is merged; the second run skips the merge and tags)",
			short(sha), strings.Join(used, ", "), sha, repo, w.number)
	}
	id, err := waiver.ParseEvidence(w.evidence, repo, w.number)
	if err == nil {
		err = waiver.CheckEvidence(get, apiBase, repo, w.number, id, sha)
	}
	if err != nil {
		return nil, fmt.Sprintf("--main-evidence: %v (condition d)", err)
	}
	return used, ""
}

func orNone(conclusion, status string) string {
	if conclusion == "" {
		return status
	}
	return conclusion
}

// stamp posts the visible record of the waiver on the pull request.
func (w *mainWaiver) stamp(token, repo, sha string, used []string) error {
	text := waiver.Stamp("Tagged "+w.tag+" on the merge commit", used, w.evidence, sha)
	resp, err := api(token, http.MethodPost, fmt.Sprintf("%s/repos/%s/issues/%d/comments", apiBase, repo, w.number), map[string]string{"body": text})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return refused("posting the waiver stamp", resp)
	}
	return nil
}
