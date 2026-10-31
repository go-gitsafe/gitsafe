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
//
// # The fourth occurrence, in a new spelling
//
// On 2026-10-10 the same mistake happened a fourth time, and not through a pipe:
//
//	ghmerge go-gitsafe/gitsafe 40                      # merged, 8/8 green
//	SHA=$(gh api repos/.../commits/main --jq .sha)     # read a MOVING ref
//	gh api -X POST .../git/refs -f sha="$SHA"
//
// A peer merged another pull request between the verification and the tag, so
// v0.6.1 landed on a commit whose checks were still running. It happened to be
// green. "Tagging a branch" above is the same error; `commits/main` is just
// another name for it.
//
// What sent someone around this command was the DRY RUN's wording. It said
// "would merge X, then tag its merge commit TAG" whatever the state, which reads
// as "will try to merge" for a pull request that is already merged -- and
// ghmerge exits 0 on one of those, so this command would have tagged the right
// commit. So the dry run now NAMES the commit it would tag. A person can then
// compare it to the sha whose checks they watched, which is the step that was
// missing.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/cliflags"
	"github.com/go-gitsafe/gitsafe/ghauth"
	"github.com/spf13/cobra"
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

// run builds the command and drives it through the adapter, so the tests reach
// the argument handling a person gets, single-dash spellings included.
func run(args []string, stdout, stderr io.Writer) int {
	cmd := RootCmd()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cliflags.Run(cmd, args)
}

type options struct {
	tokenFile, notesFile string
	dry, releaseOnly     bool
	noBackfill           bool
	backfillMax          int
}

// RootCmd returns the command, for tests to drive with SetArgs/SetOut/SetErr.
// backfill is a real subcommand, so `ghrelease backfill -h` is asked of it.
func RootCmd() *cobra.Command {
	var o options
	c := &cobra.Command{
		Use:   "ghrelease [flags] [owner/repo] <pr-number> <tag>",
		Short: "Merge a pull request, tag its merge commit and publish the release",
		// A positional that is not a verb is the repository, the number or the
		// tag; it must reach RunE rather than be refused as an unknown command.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if code := execute(args, o, cmd.OutOrStdout(), cmd.ErrOrStderr()); code != 0 {
				return cliflags.Status(code)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&o.tokenFile, "token-file", "", "the file holding the token (default ~/"+defaultToken+")")
	f.BoolVar(&o.dry, "dry-run", false, "say what would happen, merge nothing and tag nothing")
	f.StringVar(&o.notesFile, "notes-file", "", "the release notes (default: GitHub's, generated from the pull requests merged since the previous tag)")
	f.BoolVar(&o.releaseOnly, "release-only", false, "create the GitHub release of an EXISTING tag, and nothing else: "+
		"[owner/repo] <tag> (the resume after \"tag created, release missing\")")
	f.BoolVar(&o.noBackfill, "no-backfill", false, "do not create the missing releases of the repository's older tags")
	f.IntVar(&o.backfillMax, "backfill-max", defaultBackfillMax, "at most this many older tags released per run (oldest first)")
	c.AddCommand(backfillCmd())
	return c
}

func execute(args []string, o options, stdout, stderr io.Writer) int {
	tokenFile, dry, notesFile, releaseOnly, noBackfill, backfillMax :=
		&o.tokenFile, &o.dry, &o.notesFile, &o.releaseOnly, &o.noBackfill, &o.backfillMax
	var notes *string
	if *notesFile != "" {
		b, err := os.ReadFile(*notesFile)
		if err != nil {
			fmt.Fprintf(stderr, "ghrelease: cannot read the notes file: %v\n", err)
			return 2
		}
		s := string(b)
		notes = &s
	}
	if *releaseOnly {
		return resumeRelease(args, *tokenFile, notes, stdout, stderr)
	}
	repo, number, tag, err := target(args)
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
		// ⛔ This used to say only "would merge X, then tag its merge commit TAG",
		// which reads as "will try to merge" for a pull request that is ALREADY
		// merged — and ghmerge exits 0 on one of those, so ghrelease would have
		// gone straight on to tag it. On 2026-10-10 that wording sent someone
		// around this command to `gh api .../git/refs` with a sha read from
		// `commits/main`; main had moved under a peer's merge in the meantime, so
		// the tag landed on a commit whose checks were still running. It is the
		// fourth occurrence of the mistake in this file's header, in a new
		// spelling.
		//
		// So the dry run now NAMES THE COMMIT it would tag, which is both the
		// disambiguation and the thing a person can verify before acting.
		merged, sha, err := mergeState(token, repo, number)
		switch {
		case err != nil:
			// A dry run that cannot read the pull request must not pretend to
			// know. It says what it would attempt, says why it cannot be more
			// precise, and still exits 0 — refusing here would make -dry-run
			// unusable exactly when a person is being careful.
			fmt.Fprintf(stdout, "would merge %s#%d, then tag %s at its merge commit\n",
				repo, number, tag)
			fmt.Fprintf(stderr, "ghrelease: could not read whether it is already merged: %v\n", err)
			fmt.Fprintf(stderr, "ghrelease: so this did NOT name the commit it would tag\n")
		case merged && sha != "":
			fmt.Fprintf(stdout, "%s#%d is already merged; would tag %s at %s, its merge commit\n",
				repo, number, tag, short(sha))
		case merged:
			fmt.Fprintf(stderr, "ghrelease: %s#%d is merged and GitHub gave no merge commit; "+
				"there is nothing this could tag\n", repo, number)
			return 1
		default:
			fmt.Fprintf(stdout, "would merge %s#%d, then tag %s at the merge commit that "+
				"produces — it does not exist yet\n", repo, number, tag)
		}
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
	// The release is part of the job: a tag without one leaves the
	// repository's Releases page, and its Latest badge, on the previous
	// version (go-gitsafe/gitsafe#59: three times on 2026-10-26).
	if err := createRelease(token, repo, tag, notes); err != nil {
		return releaseMissing(stderr, repo, tag, err)
	}
	fmt.Fprintf(stdout, "released %s\n", tag)
	// The gradual catch-up: this repository's older tags without a release.
	// The new release is done whatever happens here, so a failure is said
	// and does not change the exit status.
	if !*noBackfill && *backfillMax > 0 {
		b := newBudget(defaultMargin)
		if _, _, err := backfillRepo(token, repo, tag, time.Time{}, *backfillMax, false, b, stdout); err != nil {
			fmt.Fprintf(stderr, "ghrelease: %s is released; catching up older tags stopped: %v\n", tag, err)
		}
	}
	return 0
}

// releaseMissing says, loudly, that the tag exists and its release does not,
// and how to finish without touching the tag.
func releaseMissing(stderr io.Writer, repo, tag string, err error) int {
	fmt.Fprintf(stderr, "ghrelease: tag created, release missing: %v\n", err)
	fmt.Fprintf(stderr, "ghrelease: the tag stays where it is; finish with:\n")
	fmt.Fprintf(stderr, "  ghrelease -release-only %s %s\n", repo, tag)
	return 1
}

// resumeRelease creates the release of a tag that already exists. It never
// creates, moves or deletes a tag: a missing tag is an error.
func resumeRelease(args []string, tokenFile string, notes *string, stdout, stderr io.Writer) int {
	var repo, tag string
	var err error
	switch len(args) {
	case 1:
		tag = args[0]
		repo, err = originRepo()
	case 2:
		repo, tag = args[0], args[1]
	default:
		err = errors.New("-release-only takes [owner/repo] <tag>")
	}
	if err == nil && !strings.HasPrefix(tag, "v") {
		err = fmt.Errorf("%q is not a version tag; Go modules want a leading v", tag)
	}
	if err != nil {
		fmt.Fprintf(stderr, "ghrelease: %v\n", err)
		return 2
	}
	token, err := token(tokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "ghrelease: %v\n", err)
		return 1
	}
	switch exists, err := tagExists(token, repo, tag); {
	case err != nil:
		fmt.Fprintf(stderr, "ghrelease: %v\n", err)
		return 1
	case !exists:
		fmt.Fprintf(stderr, "ghrelease: %s has no tag %s; -release-only does not create one\n", repo, tag)
		return 1
	}
	switch has, err := releaseExists(token, repo, tag); {
	case err != nil:
		fmt.Fprintf(stderr, "ghrelease: %v\n", err)
		return 1
	case has:
		fmt.Fprintf(stdout, "%s already has a release for %s; nothing to do\n", repo, tag)
		return 0
	}
	if err := createRelease(token, repo, tag, notes); err != nil {
		return releaseMissing(stderr, repo, tag, err)
	}
	fmt.Fprintf(stdout, "released %s (existing tag, not moved)\n", tag)
	return 0
}

// createRelease publishes the GitHub release of an existing tag (REST
// "Create a release", POST /repos/{owner}/{repo}/releases). Without notes,
// GitHub generates them from the pull requests merged since the previous
// tag (generate_release_notes). make_latest "legacy" lets GitHub mark it
// Latest only when it is the newest by date and semantic version, so a
// patch release of an older line does not take the badge.
func createRelease(token, repo, tag string, notes *string) error {
	body := map[string]any{"tag_name": tag, "name": tag, "make_latest": "legacy"}
	if notes != nil {
		body["body"] = *notes
	} else {
		body["generate_release_notes"] = true
	}
	resp, err := api(token, http.MethodPost, fmt.Sprintf("%s/repos/%s/releases", apiBase, repo), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return refused("GitHub refused the release", resp)
	}
	return nil
}

// releaseExists reports whether the tag already has a release (REST "Get a
// release by tag name").
func releaseExists(token, repo, tag string) (bool, error) {
	resp, err := api(token, http.MethodGet, fmt.Sprintf("%s/repos/%s/releases/tags/%s", apiBase, repo, tag), nil)
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
	return false, refused("looking for the release", resp)
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
	return false, refused("looking for the tag", resp)
}

// mergeState reads whether the pull request is merged and, if it is, the commit
// the merge produced.
//
// It is one reader for two callers — the dry run, which must not treat "not
// merged" as an error, and mergeCommit, which must. Two readers of the same
// endpoint is how one of them ends up decoding a field the other does not.
func mergeState(token, repo string, number int) (merged bool, sha string, err error) {
	resp, err := api(token, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/pulls/%d", apiBase, repo, number), nil)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "", refused("asking for the pull request", resp)
	}
	var pr struct {
		Merged   bool   `json:"merged"`
		MergeSHA string `json:"merge_commit_sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return false, "", err
	}
	return pr.Merged, pr.MergeSHA, nil
}

// mergeCommit is the commit the merge produced.
//
// A squash merge makes a NEW commit, so the pull request's head sha is not it
// and tagging that would tag a commit no branch contains.
func mergeCommit(token, repo string, number int) (string, error) {
	merged, sha, err := mergeState(token, repo, number)
	if err != nil {
		return "", err
	}
	if !merged {
		return "", errors.New("GitHub says the pull request is not merged")
	}
	if sha == "" {
		return "", errors.New("GitHub gave no merge commit")
	}
	return sha, nil
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
		return refused("GitHub refused the tag", resp)
	}
	return nil
}

// refused says which refusal an answer was, and while doing what.
//
// ⛔ It used to say only the status -- "GitHub answered 403 Forbidden
// looking for the tag" -- and a 403 from GitHub's burst limit, the usual
// one after a few merges in a row, then read as a dead credential: on
// 2026-10-04 a person was asked to reissue a token that was working, and
// twenty minutes later it was. ghmerge and ghscopes already ask
// ghauth.WhyRefused; now this does too.
func refused(doing string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return fmt.Errorf("%s: %w", doing, ghauth.WhyRefused(resp.StatusCode, resp.Header, body))
}
