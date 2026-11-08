package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hub is a GitHub with issues, for the issues gate. Every WRITE is recorded in
// order, so a test can say "nothing was written" and mean it.
type hub struct {
	body     string
	issues   map[string]string // "owner/repo#N" -> "open" | "closed" | "pr"
	autoShut map[string]bool   // closed by GitHub itself once the merge happened
	refuse   map[string]int    // path suffix -> status to answer a write with
	writes   []string
	gets     []string
	merged   bool
}

func newHub(body string) *hub {
	return &hub{body: body, issues: map[string]string{}, autoShut: map[string]bool{}, refuse: map[string]int{}}
}

func (h *hub) serve(t *testing.T) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if r.Method != http.MethodGet {
			h.writes = append(h.writes, r.Method+" "+p)
			if code := h.refuse[p]; code != 0 {
				w.WriteHeader(code)
				fmt.Fprint(w, `{"message":"no"}`)
				return
			}
		}
		parts := strings.Split(strings.TrimPrefix(p, "/repos/"), "/")
		switch {
		case strings.HasSuffix(p, "/merge"):
			h.merged = true
			for k := range h.autoShut {
				h.issues[k] = "closed"
			}
			fmt.Fprint(w, `{"merged":true,"sha":"0123456789abcdef0123"}`)
		case strings.Contains(p, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "check_runs": []map[string]any{
				{"name": "test", "status": "completed", "conclusion": "success"}}})
		case strings.HasSuffix(p, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"statuses": []any{}})
		case strings.Contains(p, "/git/refs/heads/"):
			w.WriteHeader(http.StatusNoContent)
		case len(parts) == 4 && parts[2] == "pulls":
			pr := green()
			pr["body"] = h.body
			_ = json.NewEncoder(w).Encode(pr)
		case len(parts) == 5 && parts[2] == "issues" && parts[4] == "comments":
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
		case len(parts) == 4 && parts[2] == "issues":
			k := fmt.Sprintf("%s/%s#%s", parts[0], parts[1], parts[3])
			if r.Method == http.MethodPatch {
				h.issues[k] = "closed"
				fmt.Fprint(w, `{}`)
				return
			}
			h.gets = append(h.gets, k)
			switch h.issues[k] {
			case "":
				w.WriteHeader(http.StatusNotFound)
			case "pr":
				fmt.Fprint(w, `{"state":"open","pull_request":{"url":"x"}}`)
			default:
				fmt.Fprintf(w, `{"state":%q}`, h.issues[k])
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	was, wasSettle := apiBase, issueSettle
	apiBase, issueSettle = s.URL, 0
	t.Cleanup(func() { apiBase, issueSettle = was, wasSettle })
	oldSleep := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = oldSleep })
}

func TestBareIssueMentionIsRefusedBeforeAnyWrite(t *testing.T) {
	h := newHub("Part of the work on #7.\n\nCloses #8")
	h.issues["go-gitsafe/gitsafe#7"] = "open"
	h.issues["go-gitsafe/gitsafe#8"] = "open"
	h.serve(t)
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 1 || h.merged || len(h.writes) != 0 {
		t.Fatalf("exit %d, merged %v, writes %v", code, h.merged, h.writes)
	}
	for _, want := range []string{"refusing to merge go-gitsafe/gitsafe#1", "open issue(s) go-gitsafe/gitsafe#7",
		"Closes go-gitsafe/gitsafe#7", "Refs go-gitsafe/gitsafe#7", "--issues-waiver"} {
		if !strings.Contains(errb, want) {
			t.Errorf("refusal lacks %q:\n%s", want, errb)
		}
	}
}

func TestRefsAndPullRequestsAndMissingNumbersDoNotRefuse(t *testing.T) {
	h := newHub("Refs #7\nsee #8 (a PR) and #9 (nothing) and #10 (closed)")
	h.issues["go-gitsafe/gitsafe#7"] = "open"
	h.issues["go-gitsafe/gitsafe#8"] = "pr"
	h.issues["go-gitsafe/gitsafe#10"] = "closed"
	h.serve(t)
	if code, _, errb := try(t, "go-gitsafe/gitsafe", "1"); code != 0 || !h.merged {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if len(h.gets) != 4 {
		t.Errorf("one GET per reference: %v", h.gets)
	}
	if strings.Join(h.writes, ",") != "PUT /repos/go-gitsafe/gitsafe/pulls/1/merge,DELETE /repos/go-gitsafe/gitsafe/git/refs/heads/a-branch" {
		t.Errorf("nothing but the merge and the branch may be written: %v", h.writes)
	}
}

func TestWaiverMergesAndIsStampedBeforeTheMerge(t *testing.T) {
	h := newHub("see #7")
	h.issues["go-gitsafe/gitsafe#7"] = "open"
	h.serve(t)
	code, out, errb := try(t, "--issues-waiver", "tracking issue, stays open", "go-gitsafe/gitsafe", "1")
	if code != 0 || !h.merged {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if len(h.writes) < 2 || h.writes[0] != "POST /repos/go-gitsafe/gitsafe/issues/1/comments" || !strings.HasSuffix(h.writes[1], "/merge") {
		t.Errorf("the stamp must come first: %v", h.writes)
	}
	if !strings.Contains(out, "WAIVED") {
		t.Errorf("not said: %s", out)
	}
}

func TestWaiverStampFailureRefusesTheMerge(t *testing.T) {
	h := newHub("see #7")
	h.issues["go-gitsafe/gitsafe#7"] = "open"
	h.refuse["/repos/go-gitsafe/gitsafe/issues/1/comments"] = http.StatusForbidden
	h.serve(t)
	if code, _, errb := try(t, "--issues-waiver", "why", "go-gitsafe/gitsafe", "1"); code != 1 || h.merged ||
		!strings.Contains(errb, "stamp could not be posted") {
		t.Errorf("exit %d merged %v: %s", code, h.merged, errb)
	}
}

func TestEmptyWaiverReasonIsRefused(t *testing.T) {
	for _, reason := range []string{"", "   "} {
		h := newHub("see #7")
		h.serve(t)
		code, _, errb := try(t, "--issues-waiver", reason, "go-gitsafe/gitsafe", "1")
		if code != 2 || len(h.gets) != 0 || len(h.writes) != 0 || !strings.Contains(errb, "needs a reason") {
			t.Errorf("reason %q: exit %d, gets %v: %s", reason, code, h.gets, errb)
		}
	}
}

func TestPostMergeCloseWhenGitHubDidNot(t *testing.T) {
	h := newHub("Fixes #3\nFixes other/repo#4\nFixes #5")
	for _, k := range []string{"go-gitsafe/gitsafe#3", "other/repo#4", "go-gitsafe/gitsafe#5"} {
		h.issues[k] = "open"
	}
	h.autoShut["go-gitsafe/gitsafe#3"] = true // same repo, default branch: GitHub closes it
	h.autoShut["go-gitsafe/gitsafe#5"] = false
	h.serve(t)
	code, out, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if h.issues["other/repo#4"] != "closed" || h.issues["go-gitsafe/gitsafe#5"] != "closed" {
		t.Errorf("left open: %v", h.issues)
	}
	for _, want := range []string{"issue go-gitsafe/gitsafe#3 closed by GitHub",
		"issue other/repo#4 was left open by GitHub; closed it (Closed by go-gitsafe/gitsafe#1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("not reported: %q in\n%s", want, out)
		}
	}
	comments := 0
	for _, w := range h.writes {
		if strings.HasPrefix(w, "POST /repos/other/repo/issues/4/comments") {
			comments++
		}
	}
	if comments != 1 {
		t.Errorf("writes %v", h.writes)
	}
}

func TestPostMergeCloseFailureIsAnErrorExitNamingTheIssue(t *testing.T) {
	h := newHub("Fixes other/repo#4")
	h.issues["other/repo#4"] = "open"
	h.refuse["/repos/other/repo/issues/4"] = http.StatusForbidden
	h.serve(t)
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != exitIssueNotClosed || !h.merged {
		t.Fatalf("exit %d merged %v", code, h.merged)
	}
	if !strings.Contains(errb, "other/repo#4 could NOT be closed") {
		t.Errorf("the issue is not named: %s", errb)
	}
}

func TestIssueReadFailureRefusesBeforeMerging(t *testing.T) {
	h := newHub("Closes #3")
	h.issues["go-gitsafe/gitsafe#3"] = "open"
	h.serve(t)
	// 500 for the issue: not a 404, so the gate cannot know and must not guess.
	srv := apiBase
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/issues/") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		resp, err := http.Get(srv + r.URL.Path)
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(bad.Close)
	apiBase = bad.URL
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 1 || h.merged || !strings.Contains(errb, "issues gate: reading go-gitsafe/gitsafe#3") {
		t.Errorf("exit %d merged %v: %s", code, h.merged, errb)
	}
}

func TestIssuesWaiverIsAFlag(t *testing.T) {
	if RootCmd().Flags().Lookup("issues-waiver") == nil {
		t.Fatal("no --issues-waiver flag")
	}
}
