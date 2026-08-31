package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// green is a pull request GitHub says is mergeable.
func green() map[string]any {
	yes := true
	return map[string]any{
		"state": "open", "merged": false, "mergeable": yes,
		"head": map[string]any{"sha": "abc", "ref": "a-branch"},
	}
}

// server answers as GitHub would, and records what it was asked to do.
func server(t *testing.T, prBody map[string]any, runs []map[string]any) (merged *bool, deleted *bool) {
	t.Helper()
	m, d := false, false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge"):
			m = true
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(r.URL.Path, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": runs})
		case strings.Contains(r.URL.Path, "/git/refs/heads/"):
			d = true
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(prBody)
		}
	}))
	t.Cleanup(s.Close)
	was := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = was })
	return &m, &d
}

// withToken points the tool at a token file that is not anybody's real one.
func withToken(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".github-token")
	if err := os.WriteFile(path, []byte("not-a-real-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func try(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"-token-file", withToken(t)}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// TestNoChecksIsNotGreen is the whole reason this exists. `gh pr checks` prints
// "no checks reported" and exits; a filter for lines that are not "pass" finds
// nothing wrong and merges. That has happened.
func TestNoChecksIsNotGreen(t *testing.T) {
	merged, _ := server(t, green(), nil)
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code == 0 {
		t.Error("merged a pull request nothing had run against")
	}
	if *merged {
		t.Error("the merge was actually sent")
	}
	if !strings.Contains(errb, "Nothing failing is not everything passing") {
		t.Errorf("the refusal does not say why:\n%s", errb)
	}
}

// TestUnmergeableIsReportedAsTheCauseNotTheSilence: a pull request that cannot
// be merged never gets a merge ref, so no workflow runs against it. Reporting
// the missing checks would send a reader to look at the wrong thing.
func TestUnmergeableIsReportedAsTheCauseNotTheSilence(t *testing.T) {
	body := green()
	no := false
	body["mergeable"] = no
	merged, _ := server(t, body, nil)
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code == 0 || *merged {
		t.Error("merged something GitHub says cannot be merged")
	}
	if !strings.Contains(errb, "cannot be merged") || !strings.Contains(errb, "no merge ref") {
		t.Errorf("the refusal names the wrong cause:\n%s", errb)
	}
}

func TestAFailingCheckIsNamed(t *testing.T) {
	runs := []map[string]any{
		{"name": "test", "status": "completed", "conclusion": "success"},
		{"name": "coverage", "status": "completed", "conclusion": "failure"},
	}
	merged, _ := server(t, green(), runs)
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code == 0 || *merged {
		t.Error("merged with a failing check")
	}
	if !strings.Contains(errb, "coverage failure") {
		t.Errorf("the failing check is not named:\n%s", errb)
	}
}

// TestAPendingCheckIsNotAPassingOne: a check still running is not evidence.
func TestAPendingCheckIsNotAPassingOne(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "in_progress"}}
	merged, _ := server(t, green(), runs)
	if code, _, errb := try(t, "go-gitsafe/gitsafe", "1"); code == 0 || *merged {
		t.Errorf("merged while a check was still running: %s", errb)
	}
}

func TestGreenMergesAndDeletesTheBranch(t *testing.T) {
	runs := []map[string]any{
		{"name": "test", "status": "completed", "conclusion": "success"},
		{"name": "cross", "status": "completed", "conclusion": "skipped"},
	}
	merged, deleted := server(t, green(), runs)
	code, out, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !*merged {
		t.Error("nothing was merged")
	}
	if !*deleted {
		t.Error("the branch was left behind")
	}
	if !strings.Contains(out, "2 check(s), all green") {
		t.Errorf("it did not say what it saw:\n%s", out)
	}
}

func TestAnAlreadyMergedPullRequestIsNotAnError(t *testing.T) {
	body := green()
	body["merged"] = true
	merged, _ := server(t, body, nil)
	if code, out, _ := try(t, "go-gitsafe/gitsafe", "1"); code != 0 || *merged {
		t.Errorf("an already-merged PR was treated as work to do: %s", out)
	}
}

func TestAClosedPullRequestIsRefused(t *testing.T) {
	body := green()
	body["state"] = "closed"
	server(t, body, nil)
	if code, _, _ := try(t, "go-gitsafe/gitsafe", "1"); code == 0 {
		t.Error("a closed pull request was merged")
	}
}

func TestTheNumberAndRepositoryAreRead(t *testing.T) {
	was := gitOutput
	t.Cleanup(func() { gitOutput = was })
	gitOutput = func(...string) (string, error) { return "https://github.com/go-gitsafe/gitsafe.git", nil }
	if repo, n, err := target([]string{"7"}); err != nil || repo != "go-gitsafe/gitsafe" || n != 7 {
		t.Errorf("target = %q,%d,%v", repo, n, err)
	}
	gitOutput = func(...string) (string, error) { return "git@github.com:go-gitsafe/gitsafe.git", nil }
	if repo, _, err := target([]string{"7"}); err != nil || repo != "go-gitsafe/gitsafe" {
		t.Errorf("an ssh remote gave %q, %v", repo, err)
	}
	if _, _, err := target([]string{"go-gitsafe/gitsafe", "12"}); err != nil {
		t.Errorf("an explicit repository: %v", err)
	}
	for _, bad := range [][]string{{}, {"zero"}, {"0"}, {"a", "b", "c"}} {
		if _, _, err := target(bad); err == nil {
			t.Errorf("target(%v) was accepted", bad)
		}
	}
	gitOutput = func(...string) (string, error) { return "", fmt.Errorf("no remote") }
	if _, _, err := target([]string{"7"}); err == nil {
		t.Error("no origin remote and no repository was accepted")
	}
	// A deeper path keeps its LAST TWO elements rather than being refused: a
	// GitHub Enterprise remote carries a prefix before owner/repo, and
	// refusing those would be strictness that helps nobody. What is refused is
	// a URL with no owner/repo in it at all, which the cases below cover.
	gitOutput = func(...string) (string, error) { return "https://example.invalid/one/two/three", nil }
	if repo, _, err := target([]string{"7"}); err != nil || repo != "two/three" {
		t.Errorf("a deeper path gave %q, %v", repo, err)
	}
	gitOutput = func(...string) (string, error) { return "https://github.com", nil }
	if _, _, err := target([]string{"7"}); err == nil {
		t.Error("a URL with no repository in it was accepted")
	}
}

func TestABadTokenFileIsRefused(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", filepath.Join(t.TempDir(), "nope"), "x/y", "1"}, &out, &errb); code == 0 {
		t.Error("a missing token file was accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty")
	_ = os.WriteFile(empty, []byte("  \n"), 0o600)
	if code := run([]string{"-token-file", empty, "x/y", "1"}, &out, &errb); code == 0 {
		t.Error("an empty token file was accepted")
	}
	if strings.Contains(errb.String(), "not-a-real-token") {
		t.Error("the error quoted the token")
	}
}

func TestBadArguments(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-nonsense"}, &out, &errb); code != 2 {
		t.Errorf("exit %d for an unknown flag, want 2", code)
	}
	if code := run([]string{}, &out, &errb); code != 2 {
		t.Errorf("exit %d for no arguments, want 2", code)
	}
}

// refusing answers every request with a status, so the error paths are exercised
// with the same shape GitHub uses when a token is wrong or a repository is gone.
func refusing(t *testing.T, code int, only string) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if only != "" && !strings.Contains(r.URL.Path, only) {
			switch {
			case strings.Contains(r.URL.Path, "/check-runs"):
				_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []map[string]any{
					{"name": "test", "status": "completed", "conclusion": "success"},
				}})
			default:
				_ = json.NewEncoder(w).Encode(green())
			}
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(s.Close)
	was := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = was })
}

func TestWhatGitHubRefusesIsReported(t *testing.T) {
	for _, c := range []struct{ name, only, want string }{
		{"the pull request", "/pulls/", "GitHub answered"},
		{"the checks", "/check-runs", "GitHub answered"},
		{"the merge", "/merge", "refused the merge"},
	} {
		t.Run(c.name, func(t *testing.T) {
			refusing(t, http.StatusInternalServerError, c.only)
			code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
			if code == 0 {
				t.Error("a refusal from GitHub was treated as success")
			}
			if !strings.Contains(errb, c.want) {
				t.Errorf("error = %q, want it to mention %q", errb, c.want)
			}
		})
	}
}

// TestABranchLeftBehindIsNotAFailure: the merge is the irreversible half. Once
// it has happened, failing because the branch survived would report the whole
// thing as not done when it is.
func TestABranchLeftBehindIsNotAFailure(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge"):
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(r.URL.Path, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": runs})
		case strings.Contains(r.URL.Path, "/git/refs/heads/"):
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_ = json.NewEncoder(w).Encode(green())
		}
	}))
	defer s.Close()
	was := apiBase
	apiBase = s.URL
	defer func() { apiBase = was }()

	code, out, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 0 {
		t.Errorf("exit %d after a successful merge: %s", code, errb)
	}
	if !strings.Contains(out, "merged") {
		t.Errorf("it did not say the merge happened:\n%s", out)
	}
	if !strings.Contains(errb, "left behind") {
		t.Errorf("it did not say the branch survived:\n%s", errb)
	}
}

// TestTheDefaultTokenIsUnderTheHome, and a home that cannot be found is an
// error rather than a path of "".
func TestTheDefaultTokenIsUnderTheHome(t *testing.T) {
	was := userHomeDir
	t.Cleanup(func() { userHomeDir = was })
	userHomeDir = func() (string, error) { return "", fmt.Errorf("no home here") }
	var out, errb bytes.Buffer
	if code := run([]string{"go-gitsafe/gitsafe", "1"}, &out, &errb); code == 0 {
		t.Error("a missing home directory was accepted")
	}
	if !strings.Contains(errb.String(), "no home here") {
		t.Errorf("error = %q", errb.String())
	}
}

// Every spelling git writes a remote in, including the one that used to be
// refused: `ssh://git@github.com/owner/repo.git`, which is what `git remote add`
// produces from an ssh:// URL and what every repository in this fleet has.
func TestRepoFromRemote(t *testing.T) {
	for _, c := range []struct {
		url, want string
	}{
		{"ssh://git@github.com/go-crdt/crdt.git", "go-crdt/crdt"},
		{"ssh://git@github.com/go-crdt/crdt", "go-crdt/crdt"},
		{"git@github.com:go-crdt/crdt.git", "go-crdt/crdt"},
		{"https://github.com/go-crdt/crdt.git", "go-crdt/crdt"},
		{"https://github.com/go-crdt/crdt", "go-crdt/crdt"},
		{"  ssh://git@github.com/go-crdt/crdt.git\n", "go-crdt/crdt"},
	} {
		got, err := repoFromRemote(c.url)
		if err != nil {
			t.Errorf("repoFromRemote(%q): %v", c.url, err)
			continue
		}
		if got != c.want {
			t.Errorf("repoFromRemote(%q) = %q, want %q", c.url, got, c.want)
		}
	}

	// And what is not a repository is still refused rather than guessed at.
	for _, url := range []string{
		"",
		"ssh://git@github.com/",
		"ssh://git@github.com/one/two/three",
		"https://example.com/go-crdt/crdt.git",
	} {
		if got, err := repoFromRemote(url); err == nil {
			t.Errorf("repoFromRemote(%q) = %q, want an error", url, got)
		}
	}
}

// TestEveryShapeOfRemote is a defect this tool hit on its first real day: it
// knew the https shape and refused every repository reached another way.
//
// The one that broke it is the last: ssh over port 443, which is what a machine
// behind a firewall that blocks port 22 uses, and which most of this fleet's
// clones are set to.
func TestEveryShapeOfRemote(t *testing.T) {
	for raw, want := range map[string]string{
		"https://github.com/go-widgets/toolkit.git":           "go-widgets/toolkit",
		"https://github.com/go-widgets/toolkit":               "go-widgets/toolkit",
		"git@github.com:go-widgets/toolkit.git":               "go-widgets/toolkit",
		"ssh://git@github.com/go-widgets/toolkit.git":         "go-widgets/toolkit",
		"ssh://git@ssh.github.com:443/go-widgets/toolkit.git": "go-widgets/toolkit",
		"  https://github.com/go-widgets/toolkit.git\n":       "go-widgets/toolkit",
	} {
		got, err := repoFromURL(raw)
		if err != nil || got != want {
			t.Errorf("repoFromURL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", "https://github.com", "nonsense", "https://github.com/only-one-part"} {
		if got, err := repoFromURL(bad); err == nil {
			t.Errorf("repoFromURL(%q) = %q, want an error", bad, got)
		}
	}
}
