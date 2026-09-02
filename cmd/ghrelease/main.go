// Command ghrelease merges a pull request and tags what it merged, as ONE
// operation.
//
// # Why this exists
//
// Merging and tagging were two commands, and three times in one day the tag
// landed on a commit that did not contain the merge. Every time the shape was
// the same:
//
//	ghmerge 21 | tail -2 && git tag v0.12.0 origin/main && gitpush origin v0.12.0
//
// ghmerge refuses correctly and exits non-zero. The PIPE throws that away: a
// pipeline's status is the status of its last command, and tail always
// succeeds. So the refusal printed, the chain continued, and a version tag was
// published pointing at a main that did not have the fix in it. A published tag
// cannot be moved -- the module proxy has it -- so each mistake cost a version
// number, permanently.
//
// A rule against this did not work; it was written down and broken three times.
// So the two steps become one command with one exit status and nothing between
// them to drop it.
//
// # What it refuses
//
// It tags the pull request's MERGE COMMIT, by its hash, not a branch name.
// Tagging a branch is what went wrong: "origin/main" was a perfectly good green
// commit that simply was not the one intended. A merge commit cannot be the
// wrong one, because it is the thing that was just merged.
//
// It refuses a tag that already exists, rather than moving it. It refuses to
// run at all if the merge did not happen, whatever the reason -- red checks, no
// checks, a conflict -- because it runs ghmerge and takes its exit status
// seriously, which is the whole point.
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

const (
	apiBase      = "https://api.github.com"
	defaultToken = ".github-token"
)

// Seams, so the tests drive a merge that refuses, a tag that exists and a
// GitHub that says no, without a network or a repository.
var (
	osExit      = os.Exit
	userHomeDir = os.UserHomeDir
	runMerge    = execMerge
	httpDo      = func(req *http.Request) (*http.Response, error) {
		return (&http.Client{Timeout: 30 * time.Second}).Do(req)
	}
)

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ghrelease", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tokenFile := fs.String("token-file", "", "the file holding the token (default ~/"+defaultToken+")")
	dry := fs.Bool("dry-run", false, "say what would happen, merge nothing and tag nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, number, tag, err := target(fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "ghrelease: %v\n", err)
		fmt.Fprintf(stderr, "usage: ghrelease [flags] [owner/repo] <pr-number> <tag>\n")
		return 2
	}
	token, err := token(*tokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "ghrelease: %v\n", err)
		return 1
	}

	// Asked BEFORE the merge. A tag that already exists cannot be moved, and
	// finding that out after merging leaves the caller with a merged pull
	// request and no release -- recoverable, but only by someone who knows
	// what half-happened.
	switch exists, err := tagExists(token, repo, tag); {
	case err != nil:
		fmt.Fprintf(stderr, "ghrelease: %v\n", err)
		return 1
	case exists:
		fmt.Fprintf(stderr, "ghrelease: %s already has the tag %s, and a published tag is not moved\n", repo, tag)
		return 1
	}

	if *dry {
		fmt.Fprintf(stdout, "would merge %s#%d, then tag its merge commit %s\n", repo, number, tag)
		return 0
	}

	if err := runMerge(repo, number, stdout, stderr); err != nil {
		// ghmerge has already said why on stderr. Saying it twice would bury
		// its reason under this one.
		fmt.Fprintf(stderr, "ghrelease: not tagging, because the merge did not happen\n")
		return 1
	}

	sha, err := mergeCommit(token, repo, number)
	if err != nil {
		fmt.Fprintf(stderr, "ghrelease: merged, but %v\n", err)
		fmt.Fprintf(stderr, "ghrelease: nothing was tagged; tag the merge commit by hand\n")
		return 1
	}
	if err := createTag(token, repo, tag, sha); err != nil {
		fmt.Fprintf(stderr, "ghrelease: merged, but %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "tagged %s at %s, the merge commit of %s#%d\n", tag, short(sha), repo, number)
	return 0
}

// short is the abbreviated hash a person reads.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// target reads the repository, the pull request number and the tag.
func target(args []string) (repo string, number int, tag string, err error) {
	switch len(args) {
	case 2:
		repo, err = originRepo()
		if err != nil {
			return "", 0, "", err
		}
		number, err = strconv.Atoi(args[0])
		tag = args[1]
	case 3:
		repo, tag = args[0], args[2]
		number, err = strconv.Atoi(args[1])
	default:
		return "", 0, "", errors.New("give a pull request number and a tag")
	}
	if err != nil {
		return "", 0, "", fmt.Errorf("%q is not a pull request number", args[len(args)-2])
	}
	if !strings.HasPrefix(tag, "v") {
		return "", 0, "", fmt.Errorf("%q is not a version tag; Go modules want a leading v", tag)
	}
	return repo, number, tag, nil
}

// originRepo reads owner/repo from the origin remote.
func originRepo() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", errors.New("no origin remote here; name the repository")
	}
	raw := strings.TrimSpace(string(out))
	raw = strings.TrimSuffix(raw, ".git")
	if i := strings.Index(raw, "github.com"); i >= 0 {
		raw = raw[i+len("github.com"):]
		raw = strings.TrimLeft(raw, ":/")
		if strings.Count(raw, "/") == 1 {
			return raw, nil
		}
	}
	return "", fmt.Errorf("cannot read a github.com repository from the origin remote")
}

// execMerge runs ghmerge and reports whether it merged.
//
// It is exec rather than a copy of the merge rules: those live in ghmerge, and
// two copies of "when may this be merged" is exactly the kind of drift that
// ends with one of them being wrong. The exit status is taken from the process,
// where nothing can eat it.
func execMerge(repo string, number int, stdout, stderr io.Writer) error {
	cmd := exec.Command("ghmerge", repo, strconv.Itoa(number))
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

func token(path string) (string, error) {
	if path == "" {
		home, err := userHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, defaultToken)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read the token file: %w", err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", fmt.Errorf("the token file is empty")
	}
	return t, nil
}

// api sends a request and returns the response, with the token in a header
// where no error message can quote it.
func api(token, method, url string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpDo(req)
	if err != nil {
		return nil, fmt.Errorf("asking GitHub: %w", err)
	}
	return resp, nil
}

// tagExists reports whether the tag is already published.
func tagExists(token, repo, tag string) (bool, error) {
	resp, err := api(token, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/git/ref/tags/%s", apiBase, repo, tag), nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("GitHub answered %s looking for the tag", resp.Status)
}

// mergeCommit is the commit the merge produced.
//
// A squash merge makes a NEW commit, so the pull request's head sha is not it
// and tagging that would tag a commit no branch contains.
func mergeCommit(token, repo string, number int) (string, error) {
	resp, err := api(token, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/pulls/%d", apiBase, repo, number), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s for the pull request", resp.Status)
	}
	var pr struct {
		Merged   bool   `json:"merged"`
		MergeSHA string `json:"merge_commit_sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", err
	}
	if !pr.Merged {
		return "", errors.New("GitHub says the pull request is not merged")
	}
	if pr.MergeSHA == "" {
		return "", errors.New("GitHub gave no merge commit")
	}
	return pr.MergeSHA, nil
}

// createTag publishes a lightweight tag at sha.
func createTag(token, repo, tag, sha string) error {
	resp, err := api(token, http.MethodPost,
		fmt.Sprintf("%s/repos/%s/git/refs", apiBase, repo),
		map[string]string{"ref": "refs/tags/" + tag, "sha": sha})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("GitHub refused the tag: %s", resp.Status)
	}
	return nil
}
