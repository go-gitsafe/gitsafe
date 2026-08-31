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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	token, err := readToken(path)
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

	runs, err := checks(token, repo, pr.Head.SHA)
	if err != nil {
		fmt.Fprintf(stderr, "ghmerge: %v\n", err)
		return 1
	}
	if why := refuse(pr, runs); why != "" {
		fmt.Fprintf(stderr, "ghmerge: refusing to merge %s#%d — %s\n", repo, number, why)
		return 1
	}

	fmt.Fprintf(stdout, "%s#%d: %d check(s), all green\n", repo, number, len(runs))
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
func refuse(pr *pr, runs []checkRun) string {
	if pr.Mergeable != nil && !*pr.Mergeable {
		return "GitHub says it cannot be merged (conflicts, most likely) — " +
			"and that is also why it has no checks: with no merge ref, no workflow runs"
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
func originRepo() (string, error) {
	url, err := gitOutput("remote", "get-url", "origin")
	if err != nil {
		return "", errors.New("no repository given and no origin remote to ask")
	}
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	url = strings.TrimPrefix(url, "https://github.com/")
	if i := strings.Index(url, "github.com:"); i >= 0 {
		url = url[i+len("github.com:"):]
	}
	if strings.Count(url, "/") != 1 || url == "" {
		return "", fmt.Errorf("cannot read owner/repo out of the origin remote")
	}
	return url, nil
}

type pr struct {
	State     string `json:"state"`
	Merged    bool   `json:"merged"`
	Mergeable *bool  `json:"mergeable"`
	Head      struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func pullRequest(token, repo string, number int) (*pr, error) {
	var out pr
	if err := get(token, fmt.Sprintf("%s/repos/%s/pulls/%d", apiBase, repo, number), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func checks(token, repo, sha string) ([]checkRun, error) {
	var out struct {
		CheckRuns []checkRun `json:"check_runs"`
	}
	url := fmt.Sprintf("%s/repos/%s/commits/%s/check-runs?per_page=100", apiBase, repo, sha)
	if err := get(token, url, &out); err != nil {
		return nil, err
	}
	return out.CheckRuns, nil
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

// readToken never puts the token in an error, so a failure cannot leak what a
// success would have protected.
func readToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return tok, nil
}
