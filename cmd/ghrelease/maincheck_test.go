package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const mergeSHA = "9454b864aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// fakeMain is a GitHub that holds one merged pull request (o/r#3 -> mergeSHA
// on main) and serves the workflow runs of that commit from a script: poll N
// gets script[min(N, len-1)]. It records the tags and releases created and the
// queries the run listing received.
type fakeMain struct {
	mu      sync.Mutex
	script  [][]string // per poll, the run objects as JSON
	polls   int
	queries []url.Values
	tagged  []string
	release []string
	prCalls int
}

func run1(name, status, conclusion string) string {
	c := "null"
	if conclusion != "" {
		c = `"` + conclusion + `"`
	}
	return fmt.Sprintf(`{"id":7,"name":%q,"status":%q,"conclusion":%s,"head_sha":%q,"html_url":"https://github.com/o/r/actions/runs/%s"}`,
		name, status, c, mergeSHA, name)
}

func (g *fakeMain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("X-RateLimit-Remaining", "4000")
	switch r.Method + " " + r.URL.Path {
	case "GET /repos/o/r/git/ref/tags/v1.0.0":
		http.NotFound(w, r)
	case "GET /repos/o/r/pulls/3":
		g.prCalls++
		fmt.Fprintf(w, `{"merged":true,"merge_commit_sha":%q,"base":{"ref":"main"}}`, mergeSHA)
	case "GET /repos/o/r/actions/runs":
		g.queries = append(g.queries, r.URL.Query())
		i := min(g.polls, len(g.script)-1)
		g.polls++
		runs := g.script[i]
		fmt.Fprintf(w, `{"total_count":%d,"workflow_runs":[%s]}`, len(runs), strings.Join(runs, ","))
	case "POST /repos/o/r/git/refs":
		g.tagged = append(g.tagged, r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	case "POST /repos/o/r/releases":
		g.release = append(g.release, r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

// fakeClock makes sleep advance now, so a 30-minute wait takes no time.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time        { return c.t }
func (c *fakeClock) sleep(d time.Duration) { c.t = c.t.Add(d) }

// scenario runs `ghrelease [args] o/r 3 v1.0.0` against a fake GitHub serving
// script, with a fake clock, and returns the exit code, what was created and
// the output.
func scenario(t *testing.T, script [][]string, args ...string) (code int, g *fakeMain, out, errs string) {
	t.Helper()
	g = &fakeMain{script: script}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	clk := &fakeClock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	oh, om, osl, on := httpDo, runMerge, sleep, now
	t.Cleanup(func() { httpDo, runMerge, sleep, now = oh, om, osl, on })
	httpDo = func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		return http.DefaultClient.Do(req)
	}
	runMerge = func(string, int, io.Writer, io.Writer) error { return nil }
	sleep, now = clk.sleep, clk.now
	var o, e bytes.Buffer
	full := append([]string{"-token-file", withToken(t, "t"), "-no-backfill"}, args...)
	full = append(full, "o/r", "3", "v1.0.0")
	code = run(full, &o, &e)
	return code, g, o.String(), e.String()
}

func TestMainGreenIsTagged(t *testing.T) {
	code, g, out, errs := scenario(t, [][]string{{run1("ci", "completed", "success"), run1("lint", "completed", "success")}})
	if code != 0 || len(g.tagged) != 1 || len(g.release) != 1 {
		t.Fatalf("exit %d, tagged %v, released %v: %s", code, g.tagged, g.release, errs)
	}
	// The listing was asked for THE merge commit on THE base branch.
	q := g.queries[0]
	if q.Get("head_sha") != mergeSHA || q.Get("branch") != "main" {
		t.Errorf("query %v", q)
	}
	if !strings.Contains(out, "CI on main is green") {
		t.Errorf("stdout %q", out)
	}
}

func TestSkippedAndNeutralAreNotFailuresAndAreSaid(t *testing.T) {
	code, g, out, errs := scenario(t, [][]string{{run1("ci", "completed", "success"),
		run1("docs", "completed", "skipped"), run1("note", "completed", "neutral")}})
	if code != 0 || len(g.tagged) != 1 {
		t.Fatalf("exit %d, tagged %v: %s", code, g.tagged, errs)
	}
	if !strings.Contains(out, "skipped or neutral, which are not failures") {
		t.Errorf("it did not say so: %q", out)
	}
}

// redMain is the scenario of 2026-11-01: the PR was green, main is red.
func redMain(t *testing.T) (int, *fakeMain, string) {
	code, g, _, errs := scenario(t, [][]string{{run1("ci", "completed", "success"), run1("fuzz-smoke", "completed", "failure")}})
	return code, g, errs
}

func TestMainRedIsNotTaggedAndNamesTheFailingRun(t *testing.T) {
	code, g, errs := redMain(t)
	if code == 0 {
		t.Error("exit 0 on a red main")
	}
	if len(g.tagged) != 0 || len(g.release) != 0 {
		t.Fatalf("tagged %v, released %v on a red main", g.tagged, g.release)
	}
	if !strings.Contains(errs, "https://github.com/o/r/actions/runs/fuzz-smoke") || strings.Contains(errs, "runs/ci") {
		t.Errorf("stderr must name the failing run, and only it: %q", errs)
	}
}

// ⛔ Planted control. The old behaviour -- tag as soon as the merge succeeds --
// is put back through the seam; the SAME red-main scenario must then see a tag.
// If it did not, TestMainRedIsNotTagged... would pass for the wrong reason (a
// fake that never serves a red run, a check that never reads g.tagged).
func TestControlTheOldBehaviourTagsARedMain(t *testing.T) {
	old := waitMain
	t.Cleanup(func() { waitMain = old })
	waitMain = func(string, string, string, string, time.Duration, bool, *budget, io.Writer, io.Writer) int { return 0 }
	code, g, _ := redMain(t)
	if code != 0 || len(g.tagged) != 1 {
		t.Fatalf("the control did not reproduce the defect: exit %d, tagged %v", code, g.tagged)
	}
}

func TestMainStillRunningThenGreenTagsAfterPolling(t *testing.T) {
	code, g, _, errs := scenario(t, [][]string{
		{run1("ci", "queued", "")},
		{run1("ci", "in_progress", "")},
		{run1("ci", "completed", "success")},
	})
	if code != 0 || len(g.tagged) != 1 {
		t.Fatalf("exit %d, tagged %v: %s", code, g.tagged, errs)
	}
	if g.polls != 3 {
		t.Errorf("polled %d times, want 3", g.polls)
	}
}

func TestMainStillRunningThenRedIsNotTagged(t *testing.T) {
	code, g, _, _ := scenario(t, [][]string{{run1("ci", "queued", "")}, {run1("ci", "completed", "cancelled")}})
	if code == 0 || len(g.tagged) != 0 {
		t.Fatalf("exit %d, tagged %v", code, g.tagged)
	}
}

func TestNoRunIsRefusedWithoutNoMainCIAndTaggedWith(t *testing.T) {
	code, g, _, errs := scenario(t, [][]string{{}})
	if code == 0 || len(g.tagged) != 0 {
		t.Fatalf("exit %d, tagged %v", code, g.tagged)
	}
	if !strings.Contains(errs, "no workflow run appeared") || !strings.Contains(errs, "-no-main-ci") {
		t.Errorf("stderr %q", errs)
	}
	code, g, _, errs = scenario(t, [][]string{{}}, "-no-main-ci")
	if code != 0 || len(g.tagged) != 1 {
		t.Fatalf("with -no-main-ci: exit %d, tagged %v: %s", code, g.tagged, errs)
	}
	if !strings.Contains(errs, "-no-main-ci was given") {
		t.Errorf("it did not say it tagged without CI: %q", errs)
	}
}

func TestARunThatAppearsWithinTheGraceIsWaitedFor(t *testing.T) {
	code, g, _, errs := scenario(t, [][]string{{}, {}, {run1("ci", "completed", "success")}})
	if code != 0 || len(g.tagged) != 1 {
		t.Fatalf("exit %d, tagged %v: %s", code, g.tagged, errs)
	}
}

func TestATimeoutIsNotTagged(t *testing.T) {
	code, g, _, errs := scenario(t, [][]string{{run1("ci", "in_progress", "")}}, "-wait-main", "5m")
	if code == 0 || len(g.tagged) != 0 {
		t.Fatalf("exit %d, tagged %v", code, g.tagged)
	}
	if !strings.Contains(errs, "had not finished after 5m0s") {
		t.Errorf("stderr %q", errs)
	}
	// Bounded: backoff, not a poll every second for the whole wait.
	if g.polls > 12 {
		t.Errorf("%d polls in 5 minutes: the backoff is not backing off", g.polls)
	}
}

func TestWaitMainZeroDoesNotWaitAndSaysSo(t *testing.T) {
	code, g, _, errs := scenario(t, [][]string{{run1("ci", "completed", "failure")}}, "-wait-main", "0")
	if code != 0 || len(g.tagged) != 1 || g.polls != 0 {
		t.Fatalf("exit %d, tagged %v, polls %d", code, g.tagged, g.polls)
	}
	if !strings.Contains(errs, "WARNING") || !strings.Contains(errs, "NOT waiting") {
		t.Errorf("stderr %q", errs)
	}
}

func TestTooManyRunsToJudgeIsNotTagged(t *testing.T) {
	// total_count 150 with one run listed: the rest were not read.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"total_count":150,"workflow_runs":[%s]}`, run1("ci", "completed", "success"))
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	oh := httpDo
	defer func() { httpDo = oh }()
	httpDo = func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		return http.DefaultClient.Do(req)
	}
	var o, e bytes.Buffer
	if code := waitMainCI("t", "o/r", mergeSHA, "main", time.Minute, false, newBudget(0), &o, &e); code == 0 {
		t.Error("tagged with 149 runs unread")
	}
}

func TestALowBudgetStopsTheWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "10")
		fmt.Fprintf(w, `{"total_count":1,"workflow_runs":[%s]}`, run1("ci", "queued", ""))
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	oh, osl := httpDo, sleep
	defer func() { httpDo, sleep = oh, osl }()
	httpDo = func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		return http.DefaultClient.Do(req)
	}
	sleep = func(time.Duration) {}
	var o, e bytes.Buffer
	if code := waitMainCI("t", "o/r", mergeSHA, "main", time.Hour, false, newBudget(defaultMargin), &o, &e); code == 0 ||
		!strings.Contains(e.String(), "REST budget") {
		t.Errorf("exit %d, stderr %q", code, e.String())
	}
}

func TestARefusedListingIsNotTagged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"You have exceeded a secondary rate limit."}`)
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	oh := httpDo
	defer func() { httpDo = oh }()
	httpDo = func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		return http.DefaultClient.Do(req)
	}
	var o, e bytes.Buffer
	if code := waitMainCI("t", "o/r", mergeSHA, "main", time.Hour, false, newBudget(0), &o, &e); code == 0 ||
		!strings.Contains(e.String(), "burst limit") {
		t.Errorf("exit %d, stderr %q", code, e.String())
	}
}

func TestNegativeWaitIsAUsageError(t *testing.T) {
	var o, e bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-wait-main", "-1s", "o/r", "3", "v1.0.0"}, &o, &e); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}
