// Command ghmerge merges one pull request, and only on evidence that it passed.
//
// It exists because "nothing is failing" is not "everything passed", and the
// difference is invisible in the obvious command. `gh pr checks` prints
// "no checks reported on the 'x' branch" and exits; a shell loop that filters
// for lines that are not "pass" then finds nothing wrong and merges. That has
// happened here.
//
// There are two ways a pull request reports no checks, and both are worth
// stopping on:
//
//   - the workflows have not started yet, which is a race with the machine;
//   - the pull request CANNOT be merged, so GitHub never created the merge ref
//     the workflows would run against, and no workflow will ever run. The
//     symptom is silence, and silence reads like success.
//
// So this refuses unless a check actually ran, every one that ran concluded
// successfully, and GitHub itself says the pull request is mergeable.
//
//	ghmerge 42                     # in a repository with an origin remote
//	ghmerge go-gitsafe/gitsafe 42
//	ghmerge -squash=false 42       # merge commit rather than squash
//	ghmerge -delete-branch=false 42
package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/go-gitsafe/gitsafe/ghauth"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// defaultToken is the file the token is read from, under the home directory.
const defaultToken = ".github-token"

var (
	osExit      = os.Exit
	userHomeDir = os.UserHomeDir
	// apiBase is a variable so a test can answer as GitHub would. What this
	// tool does with the answer is the whole of it.
	apiBase = "https://api.github.com"

	// mergeableWait is how long to pause between asking GitHub again whether a
	// pull request can be merged. A variable so a test can shorten it; nothing
	// else changes it.
	mergeableWait = 3 * time.Second
	// gitOutput asks git something, as a seam.
	gitOutput = func(args ...string) (string, error) {
		out, err := exec.Command("git", args...).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
)

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ghmerge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	squash := fs.Bool("squash", true, "squash the commits into one")
	del := fs.Bool("delete-branch", true, "delete the head branch after merging")
	tokenFile := fs.String("token-file", "", "the file holding the token (default ~/"+defaultToken+")")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, number, err := target(fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "ghmerge: %v\n", err)
		fmt.Fprintf(stderr, "usage: ghmerge [flags] [owner/repo] <number>\n")
		return 2
	}
	path := *tokenFile
	if path == "" {
		home, err := userHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "ghmerge: %v\n", err)
			return 1
		}
		path = filepath.Join(home, defaultToken)
	}
	token, err := ghauth.Read(path)
	if err != nil {
		fmt.Fprintf(stderr, "ghmerge: %v\n", err)
		return 1
	}

	pr, err := pullRequest(token, repo, number)
	if err != nil {
		fmt.Fprintf(stderr, "ghmerge: %v\n", err)
		return 1
	}
	if pr.Merged {
		fmt.Fprintf(stdout, "%s#%d is already merged\n", repo, number)
		return 0
	}
	if pr.State != "open" {
		fmt.Fprintf(stderr, "ghmerge: %s#%d is %s\n", repo, number, pr.State)
		return 1
	}

	// ⛔ mergeable: null is GitHub still THINKING, not an answer.
	//
	// It computes mergeability lazily, and the request that asks for it is
	// what starts the job. Right after a sibling pull request in the same
	// repository merges, every other one goes back to null -- and null sails
	// past the check below, which only refuses an explicit false. The merge
	// then fails with a bare "405 Method Not Allowed" that names no cause.
	//
	// That happened fifteen times in one afternoon's sweep across this fleet,
	// always in a repository where several dependency pull requests were
	// merged in sequence. Waiting a few seconds and asking again is the whole
	// fix; the answer arrived on the first retry every time it was done by
	// hand.
	for try := 0; (pr.Mergeable == nil || pr.MergeableState == "unknown") && try < 5; try++ {
		time.Sleep(mergeableWait)
		if pr, err = pullRequest(token, repo, number); err != nil {
			fmt.Fprintf(stderr, "ghmerge: %v\n", err)
			return 1
		}
	}
	if pr.Mergeable == nil || pr.MergeableState == "unknown" {
		fmt.Fprintf(stderr, "ghmerge: refusing to merge %s#%d — GitHub has not decided "+
			"whether it can be merged (mergeable is still null after 15s). It computes that "+
			"lazily and a sibling merge resets it; try again shortly.\n", repo, number)
		return 1
	}

	runs, err := checks(token, repo, pr.Head.SHA)
	if err != nil {
		fmt.Fprintf(stderr, "ghmerge: %v\n", err)
		return 1
	}
	// GitHub has TWO of these and they are different APIs. check-runs is what
	// Actions writes; the Status API is what everything else writes, and a
	// gate that reads one of them reports "all green" over a red half.
	sts, err := statuses(token, repo, pr.Head.SHA)
	if err != nil {
		fmt.Fprintf(stderr, "ghmerge: %v\n", err)
		return 1
	}
	if why := refuse(pr, runs, sts); why != "" {
		fmt.Fprintf(stderr, "ghmerge: refusing to merge %s#%d — %s\n", repo, number, why)
		return 1
	}

	fmt.Fprintf(stdout, "%s#%d: %d check(s), %d status(es), all green\n", repo, number, len(runs), len(sts))
	if err := merge(token, repo, number, *squash); err != nil {
		fmt.Fprintf(stderr, "ghmerge: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "merged\n")
	if *del {
		if err := deleteBranch(token, repo, pr.Head.Ref); err != nil {
			// Not fatal: the merge is what mattered, and a branch left behind
			// is tidied by anyone. Saying so is better than failing after the
			// irreversible half already happened.
			fmt.Fprintf(stderr, "ghmerge: the branch was left behind: %v\n", err)
			return 0
		}
		fmt.Fprintf(stdout, "branch %s deleted\n", pr.Head.Ref)
	}
	return 0
}

// refuse says why this pull request must not be merged, or "" when it may be.
//
// The order is deliberate: mergeability first, because a pull request that
// cannot be merged is ALSO the one that never gets checks, and reporting the
// silence rather than the cause would send a reader looking at the wrong thing.
func refuse(pr *pr, runs []checkRun, sts []status) string {
	if pr.Mergeable != nil && !*pr.Mergeable {
		return "GitHub says it cannot be merged (conflicts, most likely) — " +
			"and that is also why it has no checks: with no merge ref, no workflow runs"
	}
	if pr.Draft {
		return "it is a draft. GitHub will not merge one, and saying so here is " +
			"better than a 405 that names nothing"
	}
	// ⛔ mergeable:true does NOT mean "may be merged now".
	//
	// The two fields answer different questions and this tool read only the
	// first. A pull request held by branch protection -- a required review, a
	// required check that has not reported -- is mergeable:true with
	// mergeable_state:"blocked", passes every test above, and comes back from
	// the merge call as a bare "405 Method Not Allowed" that names no cause
	// and reads like a token problem.
	//
	// Only the states that MEAN something are named. "clean" is the ordinary
	// yes. "unstable" is a yes with a non-required check failing, and this
	// tool judges checks itself a few lines down, so it is left to that rather
	// than refused twice with different words. Anything else unknown is
	// allowed through, because inventing a refusal for a state GitHub adds
	// later would block work over a word this code has never seen.
	switch pr.MergeableState {
	case "blocked":
		return "branch protection is holding it: mergeable is true but " +
			"mergeable_state is \"blocked\", which means a required review or a " +
			"required check has not been satisfied. Merging it is a person's call"
	case "behind":
		return "the branch is behind its base and this repository requires them " +
			"up to date. Update it first — its checks ran against an older base " +
			"than the one it would land on"
	case "draft":
		return "GitHub reports it as a draft"
	}
	if len(runs) == 0 {
		return "no check has run against it. Nothing failing is not everything passing: " +
			"either the workflows have not started yet, or there is no merge ref for them to run against"
	}
	var bad []string
	for _, r := range runs {
		switch {
		case r.Status != "completed":
			bad = append(bad, fmt.Sprintf("%s is %s", r.Name, r.Status))
		case r.Conclusion != "success" && r.Conclusion != "neutral" && r.Conclusion != "skipped":
			bad = append(bad, fmt.Sprintf("%s %s", r.Name, r.Conclusion))
		}
	}
	// A commit status is written by whatever wrote it -- Renovate, a bot, a
	// deploy -- and carries no Status field: state is the whole answer.
	//
	// The one that actually turns up here is renovate/artifacts, and it means
	// Renovate could not update the lock files it was asked to. That is the
	// stale-go.sum failure, arriving as a signal this gate used to drop on
	// the floor: two of thirty open pull requests sampled across the fleet
	// carried a commit status at all, and both were that one, failing, on a
	// pull request whose check runs were green.
	for _, st := range sts {
		if st.State != "success" {
			bad = append(bad, fmt.Sprintf("%s %s", st.Context, st.State))
		}
	}
	if len(bad) > 0 {
		return strings.Join(bad, ", ")
	}
	return ""
}

// target reads the repository and pull-request number from the arguments,
// falling back to the origin remote of the repository this is run in.
func target(args []string) (repo string, number int, err error) {
	switch len(args) {
	case 1:
		repo, err = originRepo()
		if err != nil {
			return "", 0, err
		}
	case 2:
		repo = args[0]
		args = args[1:]
	default:
		return "", 0, errors.New("give a pull-request number")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("%q is not a pull-request number", args[0])
	}
	return repo, n, nil
}

// originRepo reads owner/repo out of the origin remote.
//
// GitHub is reached by more shapes than one, and a tool that knows only the
// https one refuses to work in half the repositories on this machine:
//
//	https://github.com/owner/repo.git
//	git@github.com:owner/repo.git
//	ssh://git@github.com/owner/repo.git
//	ssh://git@ssh.github.com:443/owner/repo.git    (port 443, for a firewall)
//
// The last one is why this was rewritten: it is what a machine behind a
// firewall that blocks port 22 uses, and it was the shape this tool met on its
// first real day.
//
// So the parsing is done by structure rather than by prefix: take everything
// after the host, and keep the last two path elements.
func originRepo() (string, error) {
	raw, err := gitOutput("remote", "get-url", "origin")
	if err != nil {
		return "", errors.New("no repository given and no origin remote to ask")
	}
	return repoFromURL(raw)
}

// repoFromURL is originRepo's arithmetic, so it can be tested against every
// shape without a repository for each.
func repoFromURL(raw string) (string, error) {
	s := strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	if s == "" {
		return "", errors.New("the origin remote has no URL")
	}
	// scp-like: git@host:owner/repo
	if i := strings.Index(s, ":"); i >= 0 && !strings.Contains(s[:i], "/") {
		if j := strings.Index(s, "://"); j < 0 {
			s = s[i+1:]
		}
	}
	// Anything with a scheme: drop it and the host, port and all.
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		j := strings.Index(rest, "/")
		if j < 0 {
			return "", fmt.Errorf("cannot read owner/repo out of the origin remote")
		}
		s = rest[j+1:]
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("cannot read owner/repo out of the origin remote")
	}
	owner, repo := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || repo == "" {
		return "", fmt.Errorf("cannot read owner/repo out of the origin remote")
	}
	return owner + "/" + repo, nil
}

// repoFromRemote reads owner/repo out of a remote URL, in every spelling git
// writes one: scp-like (git@github.com:owner/repo), https, and ssh:// with the
// userinfo in front. The third is what `git remote add` writes when it is given
// an ssh:// URL and it is not a rare shape -- every repository in this fleet has
// one -- so a parser that handles only the first two refuses the common case.
func repoFromRemote(url string) (string, error) {
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	// Everything after the host, whichever separator follows it.
	if i := strings.Index(url, "github.com"); i >= 0 {
		url = strings.TrimLeft(url[i+len("github.com"):], ":/")
	}
	if strings.Count(url, "/") != 1 || url == "" {
		return "", fmt.Errorf("cannot read owner/repo out of the origin remote")
	}
	return url, nil
}

type pr struct {
	State     string `json:"state"`
	Merged    bool   `json:"merged"`
	Draft     bool   `json:"draft"`
	Mergeable *bool  `json:"mergeable"`
	// MergeableState answers a different question from Mergeable, on the same
	// object this tool already fetches. Mergeable is "does it apply cleanly";
	// MergeableState is "may it be merged right now". A pull request behind a
	// required review is mergeable:true, mergeable_state:"blocked".
	MergeableState string `json:"mergeable_state"`
	Head           struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

type checkRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	StartedAt  string `json:"started_at"`
}

func pullRequest(token, repo string, number int) (*pr, error) {
	var out pr
	if err := get(token, fmt.Sprintf("%s/repos/%s/pulls/%d", apiBase, repo, number), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// status is an entry from the Status API, which predates check runs and is
// still what a non-Actions reporter writes. It has no Status field at all: a
// status is created in its final state, or in "pending" and replaced later.
type status struct {
	Context string `json:"context"`
	State   string `json:"state"`
}

// statuses reads the COMBINED status for a commit, which already collapses
// repeated postings of one context down to the latest.
func statuses(token, repo, sha string) ([]status, error) {
	var out struct {
		Statuses []status `json:"statuses"`
	}
	url := fmt.Sprintf("%s/repos/%s/commits/%s/status?per_page=100", apiBase, repo, sha)
	if err := get(token, url, &out); err != nil {
		return nil, err
	}
	return out.Statuses, nil
}

func checks(token, repo, sha string) ([]checkRun, error) {
	const per = 100
	var all []checkRun
	for page := 1; ; page++ {
		var out struct {
			Total     int        `json:"total_count"`
			CheckRuns []checkRun `json:"check_runs"`
		}
		url := fmt.Sprintf("%s/repos/%s/commits/%s/check-runs?per_page=%d&page=%d", apiBase, repo, sha, per, page)
		if err := get(token, url, &out); err != nil {
			return nil, err
		}
		all = append(all, out.CheckRuns...)
		// ⛔ A page that is not followed drops check runs, and the ones it
		// drops are as likely to be the failing ones as any other. A tool
		// whose whole job is to refuse a merge must not stop reading early.
		if len(out.CheckRuns) == 0 || len(all) >= out.Total {
			break
		}
	}
	return latestPerName(all), nil
}

// latestPerName keeps one run per name: the most recent.
//
// ⛔ GitHub returns EVERY run for the commit, including the one a re-run
// replaced. Measured on go-filesystems.github.io#15: two runs named "current /
// the landing names what the organisation has" on one SHA — a failure at
// 15:14:32 from a scanner version that no longer exists, and the success at
// 15:15:28 that replaced it. Reading both refuses a pull request that GitHub
// itself, branch protection and `gh pr checks` all call green, and it refuses
// every pull request that was ever red and re-run, which is most of them.
//
// The status API has no such problem, and the reason is worth keeping: this
// tool reads the COMBINED status, which already collapses repeated postings of
// one context down to the latest. Nothing collapsed the check runs.
//
// A name is the whole key, which is what branch protection matches on too. Two
// different workflows that give a job the same name are already indistinguishable
// to a required-checks rule, so this is not a new limit — but it is a limit.
func latestPerName(runs []checkRun) []checkRun {
	best := make(map[string]checkRun, len(runs))
	for _, r := range runs {
		prev, seen := best[r.Name]
		if !seen || newer(r, prev) {
			best[r.Name] = r
		}
	}
	out := slices.Collect(maps.Values(best))
	// The API's order is not promised, and a tool that names what failed must
	// name it the same way twice.
	slices.SortFunc(out, func(a, b checkRun) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// newer answers whether a started later than b. started_at is what GitHub
// orders by, and the id breaks a tie — two runs of one name in the same second
// are still two runs, and the later id is the later one.
func newer(a, b checkRun) bool {
	if a.StartedAt != b.StartedAt {
		return a.StartedAt > b.StartedAt
	}
	return a.ID > b.ID
}

func get(token, url string, into any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		// A transport error can quote the URL but never a header, so nothing
		// here can carry the token.
		return fmt.Errorf("asking GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func merge(token, repo string, number int, squash bool) error {
	method := "merge"
	if squash {
		method = "squash"
	}
	body, _ := json.Marshal(map[string]string{"merge_method": method})
	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/repos/%s/pulls/%d/merge", apiBase, repo, number), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("merging: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub refused the merge: %s", resp.Status)
	}
	return nil
}

func deleteBranch(token, repo, ref string) error {
	req, err := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/repos/%s/git/refs/heads/%s", apiBase, repo, ref), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusUnprocessableEntity {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return nil
}
