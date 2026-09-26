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
		"head":            map[string]any{"sha": "abc", "ref": "a-branch"},
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
