// Command ghnew creates a repository that already has a default branch, so no
// commit of yours ever has to land on it unreviewed.
//
// A repository created empty has no branches at all. `gh pr create` then has no
// base to open a pull request against, so the first commit cannot go through one
// -- and the way out that presents itself is to push straight to main. Four
// repositories were bootstrapped that way in one afternoon here. Each first commit
// held only a licence and a config file, and none of them was reviewed or tested,
// because there was nothing there yet to test.
//
// GitHub will make that first commit itself, and then everything of yours is a
// pull request from the beginning. That is all this does, plus refuse to believe
// it worked without looking:
//
//	ghnew -public go-compressions/adc "Apple Data Compression, pure Go"
//	ghnew -private me/notes
//	ghnew -public -clone go-filesystems/xar "the macOS .pkg container"
//
// There is deliberately no flag for creating one WITHOUT a default branch. That is
// the one thing this exists to prevent, and an option to switch it off would be an
// option to have the problem back. `gh repo create` is still there for anyone who
// genuinely wants an empty repository.
//
// It also removes a second trap on the way past: pushing a feature branch first to
// an empty repository makes GITHUB adopt that branch as the default, so the
// repository ends up with no main at all and a default branch named after whatever
// happened to go up first. That cannot happen to a repository that already has one.
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
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/ghauth"
)

// apiBase is a var so a test can point it at a server of its own.
var apiBase = "https://api.github.com"

// httpDo is a var for the same reason.
var httpDo = http.DefaultClient.Do

// cloneRoot is where -clone puts the working copy: <root>/<owner>/<name>.
//
// The default follows the layout this machine uses; GHNEW_CLONE_ROOT overrides it.
func cloneRoot() string {
	if r := os.Getenv("GHNEW_CLONE_ROOT"); r != "" {
		return r
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents", "VCS", "GIT", "github.com")
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ghnew:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ghnew", flag.ContinueOnError)
	fs.SetOutput(out)
	var (
		public    = fs.Bool("public", false, "the repository is public")
		private   = fs.Bool("private", false, "the repository is private")
		license   = fs.String("license", "bsd-3-clause", "licence template, or \"\" for none")
		gitignore = fs.String("gitignore", "", "gitignore template, e.g. Go")
		branch    = fs.String("branch", "main", "the default branch GitHub should create")
		clone     = fs.Bool("clone", false, "clone it into "+cloneRoot()+"/<owner>/<name>")
		tokenFile = fs.String("token-file", "", "the file holding the token (default ~/.github-token)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return errors.New("usage: ghnew -public|-private <owner>/<name> [description]")
	}
	// ⛔ Neither default is safe to assume. Defaulting to public publishes
	// something nobody asked to publish; defaulting to private quietly makes a
	// repository the fleet's tooling cannot see. So it is asked for.
	if *public == *private {
		return errors.New("say which: -public or -private, not both and not neither")
	}
	owner, name, err := splitRepo(fs.Arg(0))
	if err != nil {
		return err
	}
	description := ""
	if fs.NArg() == 2 {
		description = fs.Arg(1)
	}

	path := *tokenFile
	if path == "" {
		path = ghauth.DefaultPath()
	}
	token, err := ghauth.Read(path)
	if err != nil {
		return err
	}

	repo := owner + "/" + name
	if exists, err := repoExists(token, repo); err != nil {
		return err
	} else if exists {
		// Not "already done": a repository that exists may have a history, and
		// this would say nothing about whether it has a default branch either.
		return fmt.Errorf("%s already exists; this creates repositories and does not adopt them", repo)
	}

	if err := create(token, owner, name, description, *public, *license, *gitignore, *branch); err != nil {
		return err
	}
	fmt.Fprintf(out, "created %s (%s)\n", repo, visibility(*public))

	// ⛔ Looked at, not assumed. The whole point of the call above is the branch it
	// makes, and auto_init is a request rather than a guarantee -- an invalid
	// licence keyword, for one, gives a repository with no initial commit and a 201
	// for the repository itself.
	sha, err := waitForBranch(token, repo, *branch)
	if err != nil {
		return fmt.Errorf("%s was created but %w -- a pull request has no base yet, "+
			"so do NOT push to it; create the branch through the web interface or "+
			"delete the repository and try again", repo, err)
	}
	fmt.Fprintf(out, "  %s is at %s, so a pull request has a base\n", *branch, sha[:min(8, len(sha))])

	dest := filepath.Join(cloneRoot(), owner, name)
	if *clone {
		if err := gitClone(repo, dest); err != nil {
			return err
		}
		fmt.Fprintf(out, "  cloned into %s\n", dest)
	} else {
		fmt.Fprintf(out, "  clone it: git clone https://github.com/%s %s\n", repo, dest)
	}
	fmt.Fprintf(out, "  then: git checkout -b <work>, commit, gitpush origin <work>, gh pr create\n")
	return nil
}

func visibility(public bool) string {
	if public {
		return "public"
	}
	return "private"
}

// splitRepo takes owner/name apart, and refuses anything else rather than
// guessing an owner: creating a repository under the wrong account is not
// something a person notices straight away.
func splitRepo(s string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(strings.TrimSuffix(s, "/"), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("%q is not owner/name", s)
	}
	return owner, name, nil
}

// create asks GitHub for the repository, with auto_init so GitHub writes the
// first commit.
//
// The organisation endpoint is tried first and /user/repos is the fallback,
// because an owner is either an organisation or the authenticated user and
// nothing in the name says which.
func create(token, owner, name, description string, public bool, license, gitignore, branch string) error {
	body := map[string]any{
		"name":        name,
		"description": description,
		"private":     !public,
		// ⛔ The point of this program.
		"auto_init": true,
	}
	if license != "" {
		body["license_template"] = license
	}
	if gitignore != "" {
		body["gitignore_template"] = gitignore
	}
	resp, err := api(token, http.MethodPost, apiBase+"/orgs/"+owner+"/repos", body)
	if err != nil {
		return err
	}
	orgStatus, orgMsg := resp.StatusCode, readMessage(resp)
	resp.Body.Close()
	if orgStatus == http.StatusCreated {
		return renameBranch(token, owner+"/"+name, branch)
	}
	// 404 from the org endpoint means "no such organisation", which is what an
	// ordinary user account looks like from here.
	if orgStatus != http.StatusNotFound {
		return fmt.Errorf("GitHub answered %d creating %s/%s: %s", orgStatus, owner, name, orgMsg)
	}
	resp, err = api(token, http.MethodPost, apiBase+"/user/repos", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("GitHub answered %d creating %s/%s: %s",
			resp.StatusCode, owner, name, readMessage(resp))
	}
	return renameBranch(token, owner+"/"+name, branch)
}

// renameBranch moves the initial branch if the account's default name is not the
// one asked for. GitHub names it from the account setting, not from the request.
func renameBranch(token, repo, want string) error {
	if want == "" {
		return nil
	}
	got, err := defaultBranch(token, repo)
	if err != nil {
		return err
	}
	if got == want {
		return nil
	}
	resp, err := api(token, http.MethodPatch, apiBase+"/repos/"+repo, map[string]any{
		"default_branch": want,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %d renaming %s to %s: %s",
			resp.StatusCode, got, want, readMessage(resp))
	}
	return nil
}

func defaultBranch(token, repo string) (string, error) {
	resp, err := api(token, http.MethodGet, apiBase+"/repos/"+repo, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %d for %s: %s", resp.StatusCode, repo, readMessage(resp))
	}
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

// waitForBranch returns the branch's commit, giving GitHub a moment to finish
// writing the initial commit.
func waitForBranch(token, repo, branch string) (string, error) {
	var last error
	for attempt := range branchAttempts {
		if attempt > 0 {
			sleep(branchWait)
		}
		resp, err := api(token, http.MethodGet,
			apiBase+"/repos/"+repo+"/git/ref/heads/"+branch, nil)
		if err != nil {
			return "", err
		}
		var ref struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		status := resp.StatusCode
		if status == http.StatusOK {
			err = json.NewDecoder(resp.Body).Decode(&ref)
		}
		resp.Body.Close()
		if status == http.StatusOK && err == nil && ref.Object.SHA != "" {
			return ref.Object.SHA, nil
		}
		last = fmt.Errorf("%s does not exist yet (GitHub answered %d)", branch, status)
	}
	return "", last
}

// branchAttempts and branchWait are vars so a test does not have to wait.
var (
	branchAttempts = 6
	branchWait     = 500 * time.Millisecond
	sleep          = time.Sleep
)

func gitClone(repo, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "https://github.com/"+repo, dest)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

func repoExists(token, repo string) (bool, error) {
	resp, err := api(token, http.MethodGet, apiBase+"/repos/"+repo, nil)
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
	return false, fmt.Errorf("GitHub answered %d looking for %s: %s",
		resp.StatusCode, repo, readMessage(resp))
}

// api sends a request with the token in a header, where no error message can
// quote it.
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpDo(req)
	if err != nil {
		return nil, fmt.Errorf("asking GitHub: %w", err)
	}
	return resp, nil
}

// readMessage is GitHub's own explanation, which is more use than a bare status.
func readMessage(resp *http.Response) string {
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil || len(b) == 0 {
		return resp.Status
	}
	var m struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(b, &m) == nil && m.Message != "" {
		if len(m.Errors) > 0 && m.Errors[0].Message != "" {
			return m.Message + ": " + m.Errors[0].Message
		}
		return m.Message
	}
	return strings.TrimSpace(string(b))
}
