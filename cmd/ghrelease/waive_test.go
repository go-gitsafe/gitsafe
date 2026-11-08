package main

import (
	"bytes"
	"encoding/json"
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

const (
	prEvidence   = "https://github.com/o/r/pull/3#issuecomment-77"
	mainEvidence = "https://github.com/o/r/pull/3#issuecomment-88"
)

// waiveMain is a GitHub whose merge commit has a failed workflow run (id 7)
// holding an ubuntu job that passed and a macOS job no runner ever acquired.
type waiveMain struct {
	mu         sync.Mutex
	script     [][]string
	polls      int
	jobs       string // the body of GET /actions/runs/7/jobs
	me         string
	comment    string // body of the main-evidence comment (id 88); "" = 404
	stampCode  int
	events     []string
	stamps     []string
	mergeFlags []string
	// help is what `ghmerge --help` prints; requests counts every call that
	// reached GitHub; ran counts execs of ghmerge; open makes the pull request
	// not merged on its first read (so ghmerge IS run), merged otherwise.
	help     string
	requests int
	ran      int
	open     bool
	prReads  int
	// mergeErr is what the stubbed ghmerge returns.
	mergeErr error
}

const newHelp = "Usage: ghmerge [flags]\n      --waive stringArray   waive this check\n"

func jobJSON(name, conclusion string, runner string) string {
	return fmt.Sprintf(`{"id":1,"name":%q,"status":"completed","conclusion":%q,"runner_id":%s,"runner_name":null,"steps":[]}`, name, conclusion, runner)
}

func newWaiveMain() *waiveMain {
	return &waiveMain{
		script: [][]string{{run1("ci", "completed", "failure")}},
		jobs: `{"total_count":2,"jobs":[` + jobJSON("test (ubuntu)", "success", "12") + `,` +
			jobJSON("test (macos)", "cancelled", "null") + `]}`,
		me: "me", help: newHelp, open: true,
		comment:   fmt.Sprintf(`{"body":"darwin/arm64 at %s","issue_url":"https://api.github.com/repos/o/r/issues/3","user":{"login":"me"}}`, mergeSHA),
		stampCode: http.StatusCreated,
	}
}

func (g *waiveMain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests++
	w.Header().Set("X-RateLimit-Remaining", "4000")
	switch r.Method + " " + r.URL.Path {
	case "GET /repos/o/r/git/matching-refs/tags/v":
		fmt.Fprint(w, `[]`)
	case "GET /repos/o/r/git/ref/tags/v1.0.0":
		http.NotFound(w, r)
	case "GET /repos/o/r/pulls/3":
		g.prReads++
		fmt.Fprintf(w, `{"merged":%v,"merge_commit_sha":%q,"base":{"ref":"main"}}`, !(g.open && g.prReads == 1), mergeSHA)
	case "GET /repos/o/r/actions/runs":
		runs := g.script[min(g.polls, len(g.script)-1)]
		g.polls++
		fmt.Fprintf(w, `{"total_count":%d,"workflow_runs":[%s]}`, len(runs), strings.Join(runs, ","))
	case "GET /repos/o/r/actions/runs/7/jobs":
		fmt.Fprint(w, g.jobs)
	case "GET /user":
		fmt.Fprintf(w, `{"login":%q}`, g.me)
	case "GET /repos/o/r/issues/comments/88":
		if g.comment == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, g.comment)
	case "POST /repos/o/r/issues/3/comments":
		b, _ := io.ReadAll(r.Body)
		var in struct{ Body string }
		_ = json.Unmarshal(b, &in)
		g.events = append(g.events, "stamp")
		g.stamps = append(g.stamps, in.Body)
		w.WriteHeader(g.stampCode)
		fmt.Fprint(w, `{}`)
	case "POST /repos/o/r/git/refs":
		g.events = append(g.events, "tag")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	case "POST /repos/o/r/releases":
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func (g *waiveMain) tagged() bool {
	for _, e := range g.events {
		if e == "tag" {
			return true
		}
	}
	return false
}

// waiveScenario runs `ghrelease [args] o/r 3 v1.0.0`.
func waiveScenario(t *testing.T, g *waiveMain, args ...string) (code int, out, errs string) {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	clk := &fakeClock{t: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	oh, om, osl, on := httpDo, runMerge, sleep, now
	t.Cleanup(func() { httpDo, runMerge, sleep, now = oh, om, osl, on })
	httpDo = func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		return http.DefaultClient.Do(req)
	}
	runMerge = func(_ string, _ int, flags []string, _, _ io.Writer) error {
		g.mergeFlags = flags
		g.ran++
		return g.mergeErr
	}
	oldHelp := ghmergeHelp
	t.Cleanup(func() { ghmergeHelp = oldHelp })
	ghmergeHelp = func() (string, error) { return g.help, nil }
	sleep, now = clk.sleep, clk.now
	var o, e bytes.Buffer
	full := append([]string{"-token-file", withToken(t, "t"), "-no-backfill"}, args...)
	full = append(full, "o/r", "3", "v1.0.0")
	code = run(full, &o, &e)
	return code, o.String(), e.String()
}

func waiveFlags(extra ...string) []string {
	return append([]string{"--waive", "test (macos)", "--evidence", prEvidence}, extra...)
}

func TestWaiveIsPassedToGhmerge(t *testing.T) {
	g := newWaiveMain()
	g.script = [][]string{{run1("ci", "completed", "success")}}
	if code, _, errs := waiveScenario(t, g, waiveFlags("--waive", "other")...); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := "--waive test (macos) --waive other --evidence " + prEvidence
	if got := strings.Join(g.mergeFlags, " "); got != want {
		t.Errorf("ghmerge got %q, want %q", got, want)
	}
	if len(g.stamps) != 0 {
		t.Errorf("a waiver nothing needed was stamped: %q", g.stamps)
	}
}

func TestNoWaiverPassesNothingToGhmerge(t *testing.T) {
	g := newWaiveMain()
	g.script = [][]string{{run1("ci", "completed", "success")}}
	if code, _, errs := waiveScenario(t, g); code != 0 || len(g.mergeFlags) != 0 {
		t.Errorf("exit %d, flags %q: %s", code, g.mergeFlags, errs)
	}
}

func TestMainWaiverAcceptedThenStampedThenTagged(t *testing.T) {
	g := newWaiveMain()
	code, out, errs := waiveScenario(t, g, waiveFlags("--main-evidence", mainEvidence)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := strings.Join(g.events, ","); got != "stamp,tag" {
		t.Errorf("events = %s, want the stamp BEFORE the tag", got)
	}
	want := "Tagged v1.0.0 on the merge commit with 'test (macos)' waived: no runner acquired; evidence " + mainEvidence + " (local run at " + mergeSHA + ")"
	if len(g.stamps) != 1 || g.stamps[0] != want {
		t.Errorf("stamps = %q, want %q", g.stamps, want)
	}
	if !strings.Contains(out, "WAIVED on main") {
		t.Errorf("stdout does not say it waived:\n%s", out)
	}
}

// Without --main-evidence it merges (the stub), refuses to tag, and says what
// is missing, exactly.
func TestMainWaiverWithoutMainEvidenceDoesNotTag(t *testing.T) {
	g := newWaiveMain()
	code, _, errs := waiveScenario(t, g, waiveFlags()...)
	if code != 1 || g.tagged() || len(g.stamps) != 0 {
		t.Fatalf("exit %d, events %v", code, g.events)
	}
	for _, want := range []string{"evidence for the MERGE COMMIT is missing", mergeSHA, "o/r#3", "--main-evidence", "test (macos)", "nothing was tagged"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errs)
		}
	}
}

func TestMainWaiverRefusals(t *testing.T) {
	cases := []struct {
		name string
		mod  func(g *waiveMain)
		args []string
		want string
	}{
		{"a runner was acquired", func(g *waiveMain) {
			g.jobs = `{"total_count":1,"jobs":[` + jobJSON("test (macos)", "failure", "99") + `]}`
		}, nil, "a check that ran can never be waived"},
		{"another job is red and not waived", func(g *waiveMain) {
			g.jobs = `{"total_count":2,"jobs":[` + jobJSON("lint", "failure", "5") + `,` + jobJSON("test (macos)", "cancelled", "null") + `]}`
		}, nil, `job "lint" of run "ci" is failure and is not waived`},
		{"a red job with no conclusion yet", func(g *waiveMain) {
			g.jobs = `{"total_count":1,"jobs":[{"id":1,"name":"slow","status":"in_progress","conclusion":null,"runner_id":null,"steps":[]}]}`
		}, nil, `job "slow" of run "ci" is in_progress`},
		{"the run has no red job", func(g *waiveMain) {
			g.jobs = `{"total_count":1,"jobs":[` + jobJSON("test (ubuntu)", "success", "1") + `]}`
		}, nil, "none of its jobs is red"},
		{"its jobs cannot be read", func(g *waiveMain) { g.jobs = `not json` }, nil, "reading the jobs of run"},
		{"too many jobs to read", func(g *waiveMain) { g.jobs = `{"total_count":500,"jobs":[]}` }, nil, "only 0 were read"},
		{"evidence lacks the merge SHA", func(g *waiveMain) {
			g.comment = `{"body":"ran at the PR head only","issue_url":"https://api.github.com/repos/o/r/issues/3","user":{"login":"me"}}`
		}, []string{"--main-evidence", mainEvidence}, "does not contain the full SHA " + mergeSHA},
		{"evidence by someone else", func(g *waiveMain) { g.me = "you" },
			[]string{"--main-evidence", mainEvidence}, "not by the authenticated user"},
		{"evidence does not exist", func(g *waiveMain) { g.comment = "" },
			[]string{"--main-evidence", mainEvidence}, "reading the evidence comment"},
		{"evidence is not a comment of this pull request", nil,
			[]string{"--main-evidence", "https://github.com/o/r/pull/9#issuecomment-88"}, "not a comment on o/r#3"},
		{"the stamp cannot be posted", func(g *waiveMain) { g.stampCode = http.StatusForbidden },
			[]string{"--main-evidence", mainEvidence}, "stamp could not be posted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newWaiveMain()
			if c.mod != nil {
				c.mod(g)
			}
			code, _, errs := waiveScenario(t, g, waiveFlags(c.args...)...)
			if code != 1 || g.tagged() {
				t.Errorf("exit %d, tagged %v", code, g.tagged())
			}
			if !strings.Contains(errs, c.want) {
				t.Errorf("stderr lacks %q:\n%s", c.want, errs)
			}
		})
	}
}

// A failure on main with NO waiver is refused exactly as before.
func TestMainFailureWithoutAWaiverStillRefuses(t *testing.T) {
	g := newWaiveMain()
	code, _, errs := waiveScenario(t, g)
	if code != 1 || g.tagged() || !strings.Contains(errs, "CI on main failed") {
		t.Errorf("exit %d, tagged %v:\n%s", code, g.tagged(), errs)
	}
}

// A later poll must not post the stamp again.
func TestTheStampIsPostedOncePerSet(t *testing.T) {
	g := newWaiveMain()
	g.script = [][]string{
		{run1("ci", "completed", "failure"), run1("slow", "in_progress", "")},
		{run1("ci", "completed", "failure"), run1("slow", "completed", "success")},
	}
	code, _, errs := waiveScenario(t, g, waiveFlags("--main-evidence", mainEvidence)...)
	if code != 0 || len(g.stamps) != 1 {
		t.Errorf("exit %d, %d stamp(s): %s", code, len(g.stamps), errs)
	}
}

func TestWaiveFlagValidation(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"waive without evidence", []string{"--waive", "x"}, "--waive needs --evidence"},
		{"evidence without waive", []string{"--evidence", prEvidence}, "without --waive"},
		{"main-evidence without waive", []string{"--main-evidence", mainEvidence}, "without --waive"},
		{"the same name twice", []string{"--waive", "x", "--waive", "x", "--evidence", prEvidence}, "given twice"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newWaiveMain()
			code, _, errs := waiveScenario(t, g, c.args...)
			if code != 2 || !strings.Contains(errs, c.want) || len(g.mergeFlags) != 0 {
				t.Errorf("exit %d, merge flags %q:\n%s", code, g.mergeFlags, errs)
			}
		})
	}
}

// ⛔ A failed run must never be printed under "skipped or neutral". On
// textinput's first real waiver it was.
func TestAWaivedFailureIsNamedAsAFailureNotAsSkipped(t *testing.T) {
	g := newWaiveMain()
	code, out, errs := waiveScenario(t, g, waiveFlags("--main-evidence", mainEvidence)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if strings.Contains(out, "skipped or neutral") {
		t.Errorf("a failed run is listed as skipped or neutral:\n%s", out)
	}
	for _, want := range []string{
		"1 run(s) failed ONLY through the waived job(s) 'test (macos)', which never started:",
		"ci: failure",
		"apart from the waived job(s) 'test (macos)'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

// A run that failed on a job that is NOT waived is refused, waiver or not.
func TestARunFailingOnAnUnwaivedJobIsStillRefused(t *testing.T) {
	g := newWaiveMain()
	g.jobs = `{"total_count":2,"jobs":[` + jobJSON("lint", "failure", "5") + `,` + jobJSON("test (macos)", "cancelled", "null") + `]}`
	code, out, errs := waiveScenario(t, g, waiveFlags("--main-evidence", mainEvidence)...)
	if code != 1 || g.tagged() || len(g.stamps) != 0 {
		t.Errorf("exit %d, events %v", code, g.events)
	}
	if !strings.Contains(errs, `job "lint" of run "ci" is failure and is not waived`) || strings.Contains(out, "ONLY through") {
		t.Errorf("stderr:\n%s\nstdout:\n%s", errs, out)
	}
}

// An old ghmerge is refused at once, before any call to GitHub.
func TestAnOldGhmergeIsRefusedBeforeAnyNetworkCall(t *testing.T) {
	g := newWaiveMain()
	g.help = "Usage: ghmerge [flags]\n      --wait duration\n"
	code, _, errs := waiveScenario(t, g, waiveFlags("--main-evidence", mainEvidence)...)
	if code != 1 || g.requests != 0 || g.ran != 0 {
		t.Errorf("exit %d, %d request(s), %d ghmerge run(s)", code, g.requests, g.ran)
	}
	for _, want := range []string{"does not support --waive", "go install github.com/go-gitsafe/gitsafe/cmd/ghmerge@latest"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errs)
		}
	}
}

func TestAGhmergeThatCannotBeRunIsRefusedToo(t *testing.T) {
	g := newWaiveMain()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	old := ghmergeHelp
	t.Cleanup(func() { ghmergeHelp = old })
	ghmergeHelp = func() (string, error) { return "", fmt.Errorf("executable file not found") }
	var o, e bytes.Buffer
	code := run([]string{"-token-file", withToken(t, "t"), "--waive", "x", "--evidence", prEvidence, "o/r", "3", "v1.0.0"}, &o, &e)
	if code != 1 || g.requests != 0 || !strings.Contains(e.String(), "executable file not found") {
		t.Errorf("exit %d, %d request(s):\n%s", code, g.requests, e.String())
	}
}

// Without a waiver ghmerge's help is never asked: an old ghmerge still works.
func TestHelpIsNotAskedWithoutAWaiver(t *testing.T) {
	g := newWaiveMain()
	g.script = [][]string{{run1("ci", "completed", "success")}}
	g.help = ""
	if code, _, errs := waiveScenario(t, g); code != 0 {
		t.Errorf("exit %d: %s", code, errs)
	}
}

// An already merged pull request needs no ghmerge at all.
func TestAnAlreadyMergedPullRequestDoesNotExecGhmerge(t *testing.T) {
	g := newWaiveMain()
	g.open = false
	code, out, errs := waiveScenario(t, g, waiveFlags("--main-evidence", mainEvidence)...)
	if code != 0 || g.ran != 0 || !g.tagged() {
		t.Errorf("exit %d, ghmerge ran %d time(s), tagged %v: %s", code, g.ran, g.tagged(), errs)
	}
	if !strings.Contains(out, "o/r#3 is already merged") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestOrNone(t *testing.T) {
	if orNone("", "queued") != "queued" || orNone("failure", "completed") != "failure" {
		t.Error("orNone wrong")
	}
}
