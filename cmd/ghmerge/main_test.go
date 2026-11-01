package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// green is a pull request GitHub says is mergeable.
func green() map[string]any {
	yes := true
	return map[string]any{
		"state": "open", "merged": false, "mergeable": yes,
		// GitHub always sets this, so the fixture does too: a fixture that
		// leaves a field empty tests the code's handling of a shape the server
		// never sends.
		"mergeable_state": "clean",
		// head.repo is what says WHICH repository the branch is in, and
		// GitHub always sends it. Leaving it out is the very thing the
		// comment above warns about: it was missing here, so nothing
		// noticed that the branch deletion aimed at the base repository
		// with the head's ref name -- wrong for every pull request from a
		// fork.
		"head": map[string]any{
			"sha": "abc", "ref": "a-branch",
			"repo": map[string]any{"full_name": "go-gitsafe/gitsafe"},
		},
	}
}

// server answers as GitHub would, and records what it was asked to do.
// sts, when given, is the Status API's answer. Most callers pass none.
func server(t *testing.T, prBody map[string]any, runs []map[string]any, sts ...map[string]any) (merged *bool, deleted *bool) {
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
		case strings.Contains(r.URL.Path, "/actions/runs"):
			// Asked ONLY when a check run is red, to find out whether the
			// workflow run that produced it excused that job. Empty by
			// default: nothing is excused, so a test that says nothing about
			// workflow runs judges its check runs exactly as before.
			_ = json.NewEncoder(w).Encode(map[string]any{"workflow_runs": fakeWorkflowRuns})
		case strings.HasSuffix(r.URL.Path, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"statuses": sts})
		case strings.Contains(r.URL.Path, "/git/refs/heads/"):
			d = true
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "/pulls/"):
			_ = json.NewEncoder(w).Encode(prBody)
		default:
			// ⛔ This used to be `default: encode(prBody)`, and that is how the
			// Status API went unnoticed: a new endpoint got the pull request
			// back, decoded into a struct with none of those fields, and came
			// out as a valid empty answer. A fixture that agrees with any path
			// cannot tell you that you asked for one it has never heard of.
			t.Errorf("the fake GitHub was asked for %q, which it does not serve", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	was := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = was })
	t.Cleanup(func() { fakeWorkflowRuns = nil })
	return &m, &d
}

// fakeWorkflowRuns is what the fake GitHub answers for /actions/runs. server
// clears it on cleanup, so it cannot leak into the next test.
var fakeWorkflowRuns []map[string]any

// suiteRun is one workflow run over one check suite, as /actions/runs sends it.
func suiteRun(suite int64, status, conclusion string) map[string]any {
	return map[string]any{
		"check_suite_id": suite, "status": status, "conclusion": conclusion,
	}
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
	if !strings.Contains(out, "2 check(s), 0 status(es), all green") {
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
// refusing is refusingWith and no body, which is every caller that predates
// GitHub saying WHY in one.
func refusing(t *testing.T, code int, only string) {
	t.Helper()
	refusingWith(t, code, only, "")
}

// refusingWith answers `only` with a status AND a body, because the body is
// where GitHub names a burst limit. See ghauth.WhyRefused.
func refusingWith(t *testing.T, code int, only, body string) {
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
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
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

// TestAFailingCommitStatusRefuses covers the half of the signal this tool used
// to read past. GitHub keeps two independent lists against a commit: check
// runs, which Actions writes, and the Status API, which everything else does.
// ghmerge read only the first, so a pull request whose Actions lane was green
// merged while a failing status sat beside it saying otherwise.
//
// The one that actually turns up is renovate/artifacts, and it means Renovate
// could not update the lock files -- the stale go.sum that makes the NEXT
// build fail. Sampling thirty open pull requests across the fleet, two carried
// any commit status at all, and both were that one, failing.
func TestAFailingCommitStatusRefuses(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	sts := []map[string]any{{"context": "renovate/artifacts", "state": "failure"}}
	merged, _ := server(t, green(), runs, sts...)

	code, out, errOut := try(t, "go-gitsafe/gitsafe", "1")

	if code == 0 || *merged {
		t.Fatalf("merged over a failing commit status: code=%d merged=%v out=%q", code, *merged, out)
	}
	if !strings.Contains(errOut, "renovate/artifacts failure") {
		t.Errorf("the refusal must name the status and its state, got %q", errOut)
	}
}

// TestAPendingCommitStatusRefuses: a status has no separate "status" field --
// it is created in its final state, or in "pending" and replaced later. So
// pending is judged the same way an incomplete check run is.
func TestAPendingCommitStatusRefuses(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	sts := []map[string]any{{"context": "deploy/preview", "state": "pending"}}
	merged, _ := server(t, green(), runs, sts...)

	code, _, errOut := try(t, "go-gitsafe/gitsafe", "1")

	if code == 0 || *merged {
		t.Fatal("merged while a commit status was still pending")
	}
	if !strings.Contains(errOut, "deploy/preview pending") {
		t.Errorf("got %q", errOut)
	}
}

// TestASuccessfulCommitStatusStillMerges keeps the change from being a blanket
// refusal: a status that passed is a pass.
func TestASuccessfulCommitStatusStillMerges(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	sts := []map[string]any{{"context": "renovate/artifacts", "state": "success"}}
	merged, _ := server(t, green(), runs, sts...)

	code, out, errOut := try(t, "go-gitsafe/gitsafe", "1")

	if code != 0 || !*merged {
		t.Fatalf("a green status must not block: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "1 check(s), 1 status(es), all green") {
		t.Errorf("the count must show both lists, got %q", out)
	}
}

// pending is a pull request GitHub has not finished thinking about: it
// computes mergeability lazily, and reports null until it has.
func pending() map[string]any {
	return map[string]any{
		"state": "open", "merged": false, "mergeable": nil,
		"mergeable_state": "unknown",
		"head":            map[string]any{"sha": "abc", "ref": "a-branch"},
	}
}

// TestNullMergeableIsWaitedOutNotMergedThrough: null is GitHub still working,
// and it only refuses an explicit false, so null used to sail past the gate --
// and the merge came back "405 Method Not Allowed" naming no cause. That cost
// fifteen merges in one afternoon's sweep, always in a repository where
// several dependency pull requests merged in sequence, because a sibling merge
// resets every other one to null.
func TestNullMergeableIsWaitedOutNotMergedThrough(t *testing.T) {
	was := mergeableWait
	mergeableWait = time.Millisecond
	t.Cleanup(func() { mergeableWait = was })

	// null on the first ask, decided on the second -- which is what GitHub
	// actually does, and what a retry by hand saw every time.
	asked := 0
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	merged := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge"):
			merged = true
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(r.URL.Path, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": runs})
		case strings.HasSuffix(r.URL.Path, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"statuses": []map[string]any{}})
		case strings.Contains(r.URL.Path, "/git/refs/heads/"):
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "/pulls/"):
			asked++
			if asked == 1 {
				_ = json.NewEncoder(w).Encode(pending())
				return
			}
			_ = json.NewEncoder(w).Encode(green())
		default:
			t.Errorf("the fake GitHub was asked for %q, which it does not serve", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	wasBase := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = wasBase })

	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 0 || !merged {
		t.Fatalf("a pull request GitHub had merely not decided on yet was not merged: code=%d %s", code, errb)
	}
	if asked < 2 {
		t.Errorf("GitHub was asked %d time(s); a null answer must be asked again", asked)
	}
}

// TestNullMergeableForeverRefusesAndSaysWhy: if it never decides, the refusal
// has to name that, rather than handing over a bare 405 from the merge call.
func TestNullMergeableForeverRefusesAndSaysWhy(t *testing.T) {
	was := mergeableWait
	mergeableWait = time.Millisecond
	t.Cleanup(func() { mergeableWait = was })

	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	merged, _ := server(t, pending(), runs)

	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code == 0 || *merged {
		t.Fatal("merged a pull request GitHub never said was mergeable")
	}
	if !strings.Contains(errb, "has not decided") || !strings.Contains(errb, "sibling merge resets it") {
		t.Errorf("the refusal must explain the null, got:\n%s", errb)
	}
}

// held returns a pull request GitHub will not merge although it applies
// cleanly: state is the field that says so, and it is not the one this tool
// used to read.
func held(state string) map[string]any {
	b := green()
	b["mergeable_state"] = state
	return b
}

// TestBranchProtectionIsNamedNotLeftAsA405 covers the gap that turned up in
// the field: nano-container-linux/dnsd#1 was mergeable:true,
// mergeable_state:"blocked", passed every gate, and the merge came back as a
// bare "405 Method Not Allowed" — which names nothing and reads like a token
// problem. It is a person's call, and the refusal should say so.
func TestBranchProtectionIsNamedNotLeftAsA405(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	merged, _ := server(t, held("blocked"), runs)

	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")

	if code == 0 || *merged {
		t.Fatal("merged a pull request branch protection is holding")
	}
	if !strings.Contains(errb, "branch protection") || !strings.Contains(errb, "person's call") {
		t.Errorf("the refusal must name branch protection, got:\n%s", errb)
	}
}

// TestBehindIsRefusedBecauseItsChecksRanElsewhere: "behind" means the base
// moved and this repository requires branches up to date. Its green checks
// were run against a base it will not land on.
func TestBehindIsRefusedBecauseItsChecksRanElsewhere(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	merged, _ := server(t, held("behind"), runs)

	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")

	if code == 0 || *merged {
		t.Fatal("merged a branch whose checks ran against an older base")
	}
	if !strings.Contains(errb, "behind") || !strings.Contains(errb, "older base") {
		t.Errorf("got:\n%s", errb)
	}
}

// TestADraftIsRefused: GitHub will not merge one, and a named refusal beats
// discovering it from the merge call.
func TestADraftIsRefused(t *testing.T) {
	body := green()
	body["draft"] = true
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	merged, _ := server(t, body, runs)

	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")

	if code == 0 || *merged {
		t.Fatal("merged a draft")
	}
	if !strings.Contains(errb, "draft") {
		t.Errorf("got:\n%s", errb)
	}
}

// TestUnstableIsLeftToTheCheckGate: "unstable" is a yes with a non-required
// check failing. This tool judges checks itself, so refusing here as well
// would answer one fact twice in different words — and, worse, would refuse a
// pull request whose checks are all green because one of them is not required.
func TestUnstableIsLeftToTheCheckGate(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	merged, _ := server(t, held("unstable"), runs)

	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")

	if code != 0 || !*merged {
		t.Fatalf("unstable with every check green must merge: code=%d %s", code, errb)
	}
}

// TestAnUnknownStateIsNotInventedInto A Refusal: GitHub may add a word this
// code has never seen, and blocking work over it would be a refusal this tool
// made up rather than one GitHub stated.
func TestAnUnknownStateIsNotInventedIntoARefusal(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	merged, _ := server(t, held("some-state-github-added-later"), runs)

	if code, _, errb := try(t, "go-gitsafe/gitsafe", "1"); code != 0 || !*merged {
		t.Fatalf("a state this code does not know must not become a refusal: %s", errb)
	}
}

// run is one check run as GitHub reports it.
func checkrun(id int64, name, started, conclusion string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "started_at": started,
		"status": "completed", "conclusion": conclusion,
	}
}

// TestARerunReplacesTheRunItReran is the case that refused a green pull
// request. Two runs of one name on one SHA: the failure, and the re-run that
// replaced it. GitHub, branch protection and `gh pr checks` all call this
// green; reading both called it red.
func TestARerunReplacesTheRunItReran(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{
		checkrun(1, "docs", "2026-09-26T15:14:32Z", "failure"),
		checkrun(2, "docs", "2026-09-26T15:15:28Z", "success"),
	})
	code, out, errb := try(t, "o/r", "1")
	if code != 0 {
		t.Fatalf("refused a re-run that passed: %d %s%s", code, out, errb)
	}
	if !*merged {
		t.Error("not merged")
	}
	if !strings.Contains(out, "1 check(s)") {
		t.Errorf("the count still reports both runs: %q", out)
	}
}

// TestARerunThatBrokeIt is the other direction, and the one that matters: a
// green run followed by a re-run that failed must refuse. A collapse that kept
// whichever it saw first, or whichever was green, would merge this.
func TestARerunThatBrokeIt(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{
		checkrun(1, "docs", "2026-09-26T15:14:32Z", "success"),
		checkrun(2, "docs", "2026-09-26T15:15:28Z", "failure"),
	})
	code, out, errb := try(t, "o/r", "1")
	if code == 0 {
		t.Fatal("merged although the latest run failed")
	}
	if *merged {
		t.Error("merged")
	}
	if !strings.Contains(errb+out, "docs failure") {
		t.Errorf("did not name the failing check: %q", errb+out)
	}
}

// TestTheOrderInTheResponseDoesNotDecide: the same two runs, newest first.
// GitHub does not promise an order, so neither answer may depend on one.
func TestTheOrderInTheResponseDoesNotDecide(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{
		checkrun(2, "docs", "2026-09-26T15:15:28Z", "failure"),
		checkrun(1, "docs", "2026-09-26T15:14:32Z", "success"),
	})
	if code, out, errb := try(t, "o/r", "1"); code == 0 {
		t.Fatalf("merged although the latest run failed: %s%s", out, errb)
	}
	if *merged {
		t.Error("merged")
	}
}

// TestTwoRunsInTheSameSecondAreBrokenByID. started_at has one-second
// resolution, and a re-run can land inside the same second.
func TestTwoRunsInTheSameSecondAreBrokenByID(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{
		checkrun(99, "docs", "2026-09-26T15:15:28Z", "failure"),
		checkrun(7, "docs", "2026-09-26T15:15:28Z", "success"),
	})
	if code, _, _ := try(t, "o/r", "1"); code == 0 {
		t.Fatal("merged although the later id failed")
	}
	if *merged {
		t.Error("merged")
	}
}

// TestDifferentNamesAreNotCollapsed. The key is the name, so two checks that
// ran once each must both still be judged.
func TestDifferentNamesAreNotCollapsed(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{
		checkrun(1, "build", "2026-09-26T15:14:32Z", "success"),
		checkrun(2, "docs", "2026-09-26T15:14:33Z", "failure"),
	})
	code, out, errb := try(t, "o/r", "1")
	if code == 0 {
		t.Fatal("collapsed two different checks into one")
	}
	if *merged {
		t.Error("merged")
	}
	if !strings.Contains(errb+out, "docs failure") {
		t.Errorf("did not name the failing check: %q", errb+out)
	}
}

// TestASecondPageIsRead. A failure on page two is still a failure, and a tool
// that stops at page one merges through it.
func TestASecondPageIsRead(t *testing.T) {
	merged := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge"):
			merged = true
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(r.URL.Path, "/check-runs"):
			page := r.URL.Query().Get("page")
			body := map[string]any{"total_count": 101}
			if page == "1" {
				runs := make([]map[string]any, 0, 100)
				for i := range 100 {
					runs = append(runs, checkrun(int64(i), fmt.Sprintf("c%03d", i), "2026-09-26T15:00:00Z", "success"))
				}
				body["check_runs"] = runs
			} else {
				body["check_runs"] = []map[string]any{
					checkrun(100, "the-one-that-failed", "2026-09-26T15:00:00Z", "failure"),
				}
			}
			_ = json.NewEncoder(w).Encode(body)
		case strings.Contains(r.URL.Path, "/actions/runs"):
			// Asked ONLY when a check run is red, to find out whether the
			// workflow run that produced it excused that job. Empty by
			// default: nothing is excused, so a test that says nothing about
			// workflow runs judges its check runs exactly as before.
			_ = json.NewEncoder(w).Encode(map[string]any{"workflow_runs": fakeWorkflowRuns})
		case strings.HasSuffix(r.URL.Path, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"statuses": []map[string]any{}})
		case strings.Contains(r.URL.Path, "/git/refs/heads/"):
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "/pulls/"):
			_ = json.NewEncoder(w).Encode(green())
		default:
			t.Errorf("the fake GitHub was asked for %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	was := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = was })

	code, out, errb := try(t, "o/r", "1")
	if code == 0 {
		t.Fatal("merged with a failure on the second page")
	}
	if merged {
		t.Error("merged")
	}
	if !strings.Contains(errb+out, "the-one-that-failed") {
		t.Errorf("did not name it: %q", errb+out)
	}
}

// ⛔⛔ THE COMMAND THAT MEETS THE BURST LIMIT MUST NAME IT. Merging is the write
// a person does several times in a row, so ghmerge is where GitHub's burst
// limit actually bites -- and it was the one command answering nothing but
// "GitHub answered 403 Forbidden".
//
// ⭐ MEASURED: after seven pull requests merged in quick succession, ghmerge
// answered 403, and ghscopes -- asked next, to find out why -- said the token
// was probably expired or revoked. gitpush on that same token answered
// "Everything up-to-date" throughout. The credential was alive the whole time
// and the advice was to reissue it.
//
// ⚠ IT DRIVES THE COMMAND. ghauth.WhyRefused has its own table of nine cases
// and passes it, which proves the predicate and not the WIRING -- and the
// wiring is where the last defect of this shape hid.
func TestGhmergeNamesTheBurstLimit(t *testing.T) {
	const body = `{"message":"You have exceeded a secondary rate limit. ` +
		`Please wait a few minutes before you try again."}`
	for _, c := range []struct{ name, only string }{
		{"asking about the pull request", "/pulls/"},
		{"merging it", "/merge"},
	} {
		t.Run(c.name, func(t *testing.T) {
			refusingWith(t, http.StatusForbidden, c.only, body)
			code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
			if code == 0 {
				t.Fatal("a refusal from GitHub was treated as success")
			}
			if !strings.Contains(errb, "burst limit") {
				t.Errorf("error = %q, want it to name the burst limit", errb)
			}
			// ⛔ AND IT MUST NOT SEND ANYBODY TO REISSUE A WORKING CREDENTIAL,
			// which is what the old sentence did by way of ghscopes.
			if strings.Contains(errb, "expired") || strings.Contains(errb, "revoked") {
				t.Errorf("error = %q, which still blames the credential", errb)
			}
		})
	}
}

// ⛔⛔ A GREEN PULL REQUEST THAT CANNOT BE MERGED FOR TEN MINUTES used to cost a
// person a clock in their head. On 2026-11-01 two of them — 23 checks and 2
// checks, all green — were refused with a 403 that NAMED the instant the window
// reopens, and this command printed that instant and stopped. Three sessions in
// one night did the waiting by hand.
//
// ⚠ AND IT STAYS OPT-IN. Retrying a refused write is what GitHub's own
// documentation warns about, and the hourly budget here is shared with the
// fleet's Renovate runners — so the default must still refuse at once, and the
// cases below pin both halves: what --wait does, and what it does NOT do.
func TestWaitingOutARefusalGitHubSaidToWaitOut(t *testing.T) {
	const spent = `{"message":"API rate limit exceeded for user ID 11405852."}`
	const dead = `{"message":"Bad credentials"}`

	frozen := time.Date(2026, 11, 1, 10, 53, 30, 0, time.UTC)
	reset := frozen.Add(10*time.Minute + 15*time.Second)

	for _, c := range []struct {
		name    string
		args    []string
		status  int
		body    string
		reset   bool          // does the refusal carry remaining:0 and a reset?
		slept   time.Duration // what the command must wait, 0 for "not at all"
		merged  bool
		code    int
		says    string
		saysNot string
	}{
		{
			name:   "the reset GitHub named is waited out, and then it merges",
			args:   []string{"--wait", "30m"},
			status: http.StatusForbidden, body: spent, reset: true,
			slept: 10*time.Minute + 15*time.Second, merged: true, code: 0,
			says: "waiting 10m15s",
		},
		{
			// ⛔ THE DEFAULT MUST NOT HAVE CHANGED. Every other session on this
			// machine calls `ghmerge <n>` with no flags, and a tool that
			// silently started sleeping for ten minutes would be worse than
			// one that refuses.
			name:   "with no --wait it still refuses at once",
			status: http.StatusForbidden, body: spent, reset: true,
			slept: 0, merged: false, code: 1,
			says: "exhausted until", saysNot: "waiting",
		},
		{
			// A --wait that cannot reach the deadline must not sleep for as
			// much of it as it can and then fail: that spends the wait and
			// still refuses.
			name:   "a --wait that does not reach the deadline sleeps not at all",
			args:   []string{"--wait", "1m"},
			status: http.StatusForbidden, body: spent, reset: true,
			slept: 0, merged: false, code: 1,
			says: "--wait 1m0s does not reach it",
		},
		{
			// ⛔ Waiting cannot revive a token. Answering "in ten minutes"
			// here would build a loop with no success in it.
			name:   "a dead credential is refused however large --wait is",
			args:   []string{"--wait", "30m"},
			status: http.StatusUnauthorized, body: dead,
			slept: 0, merged: false, code: 1,
			says: "refused the credential", saysNot: "waiting",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			refusals, merged := 0, false
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/merge"):
					if refusals == 0 {
						refusals++
						if c.reset {
							w.Header().Set("X-RateLimit-Remaining", "0")
							w.Header().Set("X-RateLimit-Resource", "core")
							w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
						}
						w.WriteHeader(c.status)
						_, _ = w.Write([]byte(c.body))
						return
					}
					merged = true
					fmt.Fprint(w, `{"merged":true}`)
				case strings.Contains(r.URL.Path, "/check-runs"):
					_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []map[string]any{
						{"name": "test", "status": "completed", "conclusion": "success"},
					}})
				case strings.HasSuffix(r.URL.Path, "/status"):
					_ = json.NewEncoder(w).Encode(map[string]any{"statuses": []map[string]any{}})
				case strings.Contains(r.URL.Path, "/git/refs/heads/"):
					w.WriteHeader(http.StatusNoContent)
				default:
					_ = json.NewEncoder(w).Encode(green())
				}
			}))
			t.Cleanup(s.Close)
			wasBase, wasSleep, wasNow := apiBase, sleep, now
			apiBase = s.URL
			var slept []time.Duration
			sleep = func(d time.Duration) { slept = append(slept, d) }
			now = func() time.Time { return frozen }
			t.Cleanup(func() { apiBase, sleep, now = wasBase, wasSleep, wasNow })

			code, _, errb := try(t, append(c.args, "go-gitsafe/gitsafe", "1")...)
			if code != c.code {
				t.Errorf("exit %d, want %d; stderr:\n%s", code, c.code, errb)
			}
			if merged != c.merged {
				t.Errorf("merged = %v, want %v", merged, c.merged)
			}
			var total time.Duration
			for _, d := range slept {
				total += d
			}
			if total != c.slept {
				t.Errorf("slept %v in %d call(s), want %v", total, len(slept), c.slept)
			}
			if !strings.Contains(errb, c.says) {
				t.Errorf("stderr does not say %q:\n%s", c.says, errb)
			}
			if c.saysNot != "" && strings.Contains(errb, c.saysNot) {
				t.Errorf("stderr says %q, which it must not:\n%s", c.saysNot, errb)
			}
		})
	}
}

// ⛔ AND THE WAIT IS NEVER SHORTER THAN A MINUTE, even when the instant has
// already gone by. A stale reset plus an eager floor is a loop that spends the
// shared budget hammering a window that is still shut; GitHub's own floor for a
// burst refusal is "at least one minute".
func TestAResetAlreadyPassedStillWaitsAMinute(t *testing.T) {
	frozen := time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC)
	refusals := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge"):
			if refusals == 0 {
				refusals++
				w.Header().Set("X-RateLimit-Remaining", "0")
				// Five minutes BEFORE the frozen clock.
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(frozen.Add(-5*time.Minute).Unix(), 10))
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
				return
			}
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(r.URL.Path, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []map[string]any{
				{"name": "test", "status": "completed", "conclusion": "success"},
			}})
		case strings.HasSuffix(r.URL.Path, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"statuses": []map[string]any{}})
		case strings.Contains(r.URL.Path, "/git/refs/heads/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(green())
		}
	}))
	t.Cleanup(s.Close)
	wasBase, wasSleep, wasNow := apiBase, sleep, now
	apiBase = s.URL
	var slept []time.Duration
	sleep = func(d time.Duration) { slept = append(slept, d) }
	now = func() time.Time { return frozen }
	t.Cleanup(func() { apiBase, sleep, now = wasBase, wasSleep, wasNow })

	code, _, errb := try(t, "--wait", "5m", "go-gitsafe/gitsafe", "1")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr:\n%s", code, errb)
	}
	if len(slept) != 1 || slept[0] != time.Minute {
		t.Errorf("slept %v, want exactly one minute", slept)
	}
}

// TestAForkBranchIsLeftAlone is the defect this fixture used to hide. The
// head branch of a pull request from a fork lives in the CONTRIBUTOR's
// repository; the delete URL named the base repository with that
// branch's name, so a fork branch called `main` -- or any name the base
// also uses -- aimed the request at our own branch of that name.
//
// Seen for real, merging an outside pull request into go-ansible/template
// whose head ref was `main`: ghmerge sent a DELETE at the base's own main
// and announced "branch main deleted". Only GitHub's refusal to delete a
// default branch stopped it.
func TestAForkBranchIsLeftAlone(t *testing.T) {
	pr := green()
	pr["head"] = map[string]any{
		"sha": "abc", "ref": "main",
		"repo": map[string]any{"full_name": "a-contributor/gitsafe"},
	}
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	merged, deleted := server(t, pr, runs)

	code, out, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !*merged {
		t.Error("the merge itself must still happen")
	}
	if *deleted {
		t.Error("it deleted a branch in a repository that is not this one")
	}
	if !strings.Contains(out, "left alone") || !strings.Contains(out, "a-contributor/gitsafe") {
		t.Errorf("it did not say whose branch it left alone:\n%s", out)
	}
}

// A head whose fork has been deleted sends head.repo as null. There is
// nothing to delete, and nothing to aim at this repository either.
func TestAVanishedForkIsNotDeletedFromHere(t *testing.T) {
	pr := green()
	pr["head"] = map[string]any{"sha": "abc", "ref": "a-branch", "repo": nil}
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	_, deleted := server(t, pr, runs)

	if code, _, errb := try(t, "go-gitsafe/gitsafe", "1"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if *deleted {
		t.Error("it tried to delete a branch for a head repository that is gone")
	}
}

// 422 means there was no ref to delete. It is an acceptable outcome, not a
// deletion -- and reporting it as "deleted" is how this tool announced the
// removal of a branch that is still standing.
func TestAnAbsentBranchIsNotReportedAsDeleted(t *testing.T) {
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge"):
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(r.URL.Path, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": runs})
		case strings.Contains(r.URL.Path, "/git/refs/heads/"):
			w.WriteHeader(http.StatusUnprocessableEntity)
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
		t.Fatalf("exit %d: %s", code, errb)
	}
	if strings.Contains(out, "deleted") {
		t.Errorf("422 was announced as a deletion:\n%s", out)
	}
	if !strings.Contains(out, "already gone") {
		t.Errorf("it did not say what actually happened:\n%s", out)
	}
}

// TestAnAdvisoryLaneDoesNotBlockTheMerge is go-gfx/gfx#74, reduced.
//
// Twelve lanes green, one red, and the workflow run that produced all thirteen
// concluded SUCCESS -- which GitHub does with a failed job only when that job
// carried `continue-on-error`. Before this, every advisory lane in the fleet
// made its pull request permanently unmergeable by this tool, although the lane
// was red on purpose.
func TestAnAdvisoryLaneDoesNotBlockTheMerge(t *testing.T) {
	fakeWorkflowRuns = []map[string]any{suiteRun(7, "completed", "success")}
	merged, _ := server(t, green(), []map[string]any{
		{"id": 1, "name": "gofmt", "status": "completed", "conclusion": "success",
			"check_suite": map[string]any{"id": 7}},
		{"id": 2, "name": "arch loong64 (qemu)", "status": "completed", "conclusion": "failure",
			"check_suite": map[string]any{"id": 7}},
	})
	code, out, errb := try(t, "o/r", "1")
	if code != 0 || !*merged {
		t.Errorf("an advisory lane blocked the merge: code=%d merged=%v\n%s", code, *merged, errb)
	}
	// A dispensation must never be SILENT. A lane quietly marked
	// continue-on-error to get a merge through has to be visible in this
	// tool's output, not inferred from it going quiet.
	if !strings.Contains(out, "ADVISORY") || !strings.Contains(out, "arch loong64 (qemu)") {
		t.Errorf("the dispensation was not named in the output:\n%s", out)
	}
}

// TestARedLaneInAFailedRunStillRefuses is the negative control, and it is the
// half that matters: widening a gate must not widen it to everything.
//
// The same red check in the same suite -- but the run concluded FAILURE, so
// nothing was excused and the refusal stands.
func TestARedLaneInAFailedRunStillRefuses(t *testing.T) {
	fakeWorkflowRuns = []map[string]any{suiteRun(7, "completed", "failure")}
	merged, _ := server(t, green(), []map[string]any{
		{"id": 1, "name": "gofmt", "status": "completed", "conclusion": "success",
			"check_suite": map[string]any{"id": 7}},
		{"id": 2, "name": "test", "status": "completed", "conclusion": "failure",
			"check_suite": map[string]any{"id": 7}},
	})
	if code, _, errb := try(t, "o/r", "1"); code == 0 || *merged {
		t.Errorf("a red lane inside a FAILED run was merged: %s", errb)
	}
}

// TestAnUnfinishedRunExcusesNothing: a run still going has concluded nothing,
// and reading an empty conclusion as a dispensation would merge over a lane
// that is about to go red.
func TestAnUnfinishedRunExcusesNothing(t *testing.T) {
	fakeWorkflowRuns = []map[string]any{suiteRun(7, "in_progress", "")}
	merged, _ := server(t, green(), []map[string]any{
		{"id": 2, "name": "test", "status": "completed", "conclusion": "failure",
			"check_suite": map[string]any{"id": 7}},
	})
	if code, _, errb := try(t, "o/r", "1"); code == 0 || *merged {
		t.Errorf("an unfinished run excused a failure: %s", errb)
	}
}

// TestADispensationIsNotTransferredBetweenSuites: one run concluding success
// does not excuse a red check belonging to a DIFFERENT suite. The join is the
// suite id and nothing looser -- a commit can carry several workflow runs, and
// one of them passing says nothing about another's jobs.
func TestADispensationIsNotTransferredBetweenSuites(t *testing.T) {
	fakeWorkflowRuns = []map[string]any{suiteRun(7, "completed", "success")}
	merged, _ := server(t, green(), []map[string]any{
		{"id": 2, "name": "test", "status": "completed", "conclusion": "failure",
			"check_suite": map[string]any{"id": 9}},
	})
	if code, _, errb := try(t, "o/r", "1"); code == 0 || *merged {
		t.Errorf("a dispensation crossed from suite 7 to suite 9: %s", errb)
	}
}

// TestTheGreenPathAsksForNoWorkflowRuns pins the budget decision. The hourly
// REST allowance is shared with Renovate and every other session on this
// machine, so the ordinary green merge must not pay for a call whose answer it
// cannot use.
func TestTheGreenPathAsksForNoWorkflowRuns(t *testing.T) {
	asked := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/actions/runs"):
			asked = true
			_ = json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []any{}})
		case strings.HasSuffix(r.URL.Path, "/merge"):
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(r.URL.Path, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "check_runs": []map[string]any{
				{"id": 1, "name": "gofmt", "status": "completed", "conclusion": "success",
					"check_suite": map[string]any{"id": 7}},
			}})
		case strings.HasSuffix(r.URL.Path, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"statuses": []map[string]any{}})
		case strings.Contains(r.URL.Path, "/git/refs/heads/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(green())
		}
	}))
	t.Cleanup(s.Close)
	was := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = was })
	if code, _, errb := try(t, "o/r", "1"); code != 0 {
		t.Fatalf("a green merge failed: %s", errb)
	}
	if asked {
		t.Error("the green path asked for /actions/runs; that answer buys nothing " +
			"when no check is red, and the budget is shared")
	}
}
