package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const headSHA = "0123456789abcdef0123456789abcdef01234567"

const evidenceURL = "https://github.com/go-gitsafe/gitsafe/pull/1#issuecomment-77"

// waiveFake is a GitHub with one pull request whose macOS job never got a
// runner, and an evidence comment that satisfies the rule until a test breaks
// one condition.
type waiveFake struct {
	runs        []map[string]any
	jobs        map[int64]map[string]any
	comment     map[string]any // nil: 404
	me          string
	stampStatus int
	events      []string // "stamp", "merge", in order
	stamps      []string
	mergeStatus int
}

func newWaiveFake() *waiveFake {
	return &waiveFake{
		runs: []map[string]any{
			checkrun(1, "test (ubuntu-latest)", "2026-11-04T10:00:00Z", "success"),
			checkrun(2, "test (macos-latest)", "2026-11-04T10:00:00Z", "cancelled"),
		},
		jobs: map[int64]map[string]any{2: unstartedJob(2)},
		comment: map[string]any{
			"body":      "go test ./... on darwin/arm64 at " + headSHA + ": ok",
			"issue_url": "https://api.github.com/repos/go-gitsafe/gitsafe/issues/1",
			"user":      map[string]any{"login": "Me"},
		},
		me: "me", stampStatus: http.StatusCreated, mergeStatus: http.StatusOK,
	}
}

// unstartedJob is what GitHub says of a job no runner ever acquired.
func unstartedJob(id int64) map[string]any {
	return map[string]any{
		"id": id, "name": "test (macos-latest)", "status": "completed", "conclusion": "cancelled",
		"runner_id": nil, "runner_name": nil, "steps": []any{},
	}
}

func (f *waiveFake) serve(t *testing.T) {
	t.Helper()
	body := green()
	body["head"].(map[string]any)["sha"] = headSHA
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(p, "/merge"):
			f.events = append(f.events, "merge")
			w.WriteHeader(f.mergeStatus)
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(p, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(f.runs), "check_runs": f.runs})
		case strings.Contains(p, "/actions/runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []any{}})
		case strings.Contains(p, "/actions/jobs/"):
			id, _ := strconv.ParseInt(p[strings.LastIndex(p, "/")+1:], 10, 64)
			if j, ok := f.jobs[id]; ok {
				_ = json.NewEncoder(w).Encode(j)
				return
			}
			http.NotFound(w, r)
		case p == "/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"login": f.me})
		case strings.HasPrefix(p, "/repos/go-gitsafe/gitsafe/issues/comments/"):
			if f.comment == nil || !strings.HasSuffix(p, "/77") {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(f.comment)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/issues/1/comments"):
			b, _ := io.ReadAll(r.Body)
			var in struct{ Body string }
			_ = json.Unmarshal(b, &in)
			f.events = append(f.events, "stamp")
			f.stamps = append(f.stamps, in.Body)
			w.WriteHeader(f.stampStatus)
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(p, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"statuses": []any{}})
		case strings.Contains(p, "/git/refs/heads/"):
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(p, "/pulls/"):
			_ = json.NewEncoder(w).Encode(body)
		default:
			t.Errorf("the fake GitHub was asked for %s %q, which it does not serve", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	was := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = was })
}

func (f *waiveFake) merged() bool {
	for _, e := range f.events {
		if e == "merge" {
			return true
		}
	}
	return false
}

func waiveArgs(extra ...string) []string {
	return append([]string{"--waive", "test (macos-latest)", "--evidence", evidenceURL, "go-gitsafe/gitsafe", "1"}, extra...)
}

// The accepted path, with a job that has no steps at all.
func TestWaiveAcceptsAJobNoRunnerAcquired(t *testing.T) {
	f := newWaiveFake()
	f.serve(t)
	code, out, errb := try(t, waiveArgs()...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := strings.Join(f.events, ","); got != "stamp,merge" {
		t.Errorf("events = %s, want the stamp BEFORE the merge", got)
	}
	want := "Merged with 'test (macos-latest)' waived: no runner acquired; evidence " + evidenceURL + " (local run at " + headSHA + ")"
	if len(f.stamps) != 1 || f.stamps[0] != want {
		t.Errorf("stamp = %q, want %q", f.stamps, want)
	}
	if !strings.Contains(out, "WAIVED") {
		t.Errorf("stdout does not say it waived:\n%s", out)
	}
}

// The accepted path, with a job whose steps are all still queued.
func TestWaiveAcceptsQueuedStepsThatNeverBegan(t *testing.T) {
	f := newWaiveFake()
	f.jobs[2]["steps"] = []any{
		map[string]any{"name": "Set up job", "status": "queued", "conclusion": nil},
		map[string]any{"name": "Run tests", "status": "queued", "conclusion": nil},
	}
	f.jobs[2]["runner_id"] = 0
	f.serve(t)
	if code, _, errb := try(t, waiveArgs()...); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !f.merged() {
		t.Error("not merged")
	}
}

func TestWaiveRefusals(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(f *waiveFake)
		args   []string
		code   int
		want   string
	}{
		{name: "a: no such check", args: []string{"--waive", "nope", "--evidence", evidenceURL, "go-gitsafe/gitsafe", "1"},
			code: 1, want: "condition a"},
		{name: "b: a runner was acquired", break_: func(f *waiveFake) { f.jobs[2]["runner_id"] = 9 },
			code: 1, want: "a check that ran can never be waived"},
		{name: "b: a runner name", break_: func(f *waiveFake) { f.jobs[2]["runner_name"] = "GitHub Actions 3" },
			code: 1, want: "condition b"},
		{name: "b: a step started and failed", break_: func(f *waiveFake) {
			f.jobs[2]["steps"] = []any{map[string]any{"name": "Run tests", "status": "completed", "conclusion": "failure", "started_at": "2026-11-04T10:01:00Z"}}
		}, code: 1, want: `step "Run tests" started`},
		{name: "b: a step with only a start time", break_: func(f *waiveFake) {
			f.jobs[2]["steps"] = []any{map[string]any{"name": "Set up job", "status": "in_progress", "started_at": "2026-11-04T10:01:00Z"}}
		}, code: 1, want: "started"},
		{name: "b: it is green", break_: func(f *waiveFake) { f.jobs[2]["conclusion"] = "success" },
			code: 1, want: "nothing to waive"},
		{name: "b: it is still running", break_: func(f *waiveFake) { f.jobs[2]["status"] = "in_progress" },
			code: 1, want: "not finished"},
		{name: "b: its job cannot be read", break_: func(f *waiveFake) { delete(f.jobs, 2) },
			code: 1, want: "reading its job"},
		{name: "c: another check is red", break_: func(f *waiveFake) {
			f.runs[0] = checkrun(1, "test (ubuntu-latest)", "2026-11-04T10:00:00Z", "failure")
		}, code: 1, want: "test (ubuntu-latest) failure"},
		{name: "c: another check is pending", break_: func(f *waiveFake) {
			f.runs[0]["status"] = "in_progress"
		}, code: 1, want: "in_progress"},
		{name: "d: not a URL for this pull request", args: []string{"--waive", "test (macos-latest)", "--evidence", "https://github.com/go-gitsafe/gitsafe/pull/2#issuecomment-77", "go-gitsafe/gitsafe", "1"},
			code: 1, want: "not a comment on go-gitsafe/gitsafe#1"},
		{name: "d: a URL naming no comment", args: []string{"--waive", "test (macos-latest)", "--evidence", "https://github.com/go-gitsafe/gitsafe/pull/1", "go-gitsafe/gitsafe", "1"},
			code: 1, want: "names no comment"},
		{name: "d: not github", args: []string{"--waive", "test (macos-latest)", "--evidence", "https://example.com/x#issuecomment-77", "go-gitsafe/gitsafe", "1"},
			code: 1, want: "is not an https://github.com"},
		{name: "d: the comment does not exist", break_: func(f *waiveFake) { f.comment = nil },
			code: 1, want: "reading the evidence comment"},
		{name: "d: written by someone else", break_: func(f *waiveFake) { f.me = "someone-else" },
			code: 1, want: "not by the authenticated user"},
		{name: "d: no SHA in it", break_: func(f *waiveFake) { f.comment["body"] = "tests passed locally" },
			code: 1, want: "does not contain the full SHA " + headSHA},
		{name: "d: only the short SHA", break_: func(f *waiveFake) { f.comment["body"] = "ok at " + headSHA[:7] },
			code: 1, want: "does not contain the full SHA"},
		{name: "d: another commit's SHA", break_: func(f *waiveFake) { f.comment["body"] = "ok at " + strings.Repeat("a", 40) },
			code: 1, want: "does not contain the full SHA"},
		{name: "d: the comment is on another issue", break_: func(f *waiveFake) {
			f.comment["issue_url"] = "https://api.github.com/repos/go-gitsafe/gitsafe/issues/11"
		}, code: 1, want: "is not on go-gitsafe/gitsafe#1"},
		{name: "the stamp cannot be posted", break_: func(f *waiveFake) { f.stampStatus = http.StatusForbidden },
			code: 1, want: "stamp could not be posted"},
		{name: "evidence is required", args: []string{"--waive", "test (macos-latest)", "go-gitsafe/gitsafe", "1"},
			code: 2, want: "--waive needs --evidence"},
		{name: "evidence alone", args: []string{"--evidence", evidenceURL, "go-gitsafe/gitsafe", "1"},
			code: 2, want: "without --waive"},
		{name: "an empty name", args: []string{"--waive", " ", "--evidence", evidenceURL, "go-gitsafe/gitsafe", "1"},
			code: 2, want: "needs a check name"},
		{name: "a name twice", args: []string{"--waive", "x", "--waive", "x", "--evidence", evidenceURL, "go-gitsafe/gitsafe", "1"},
			code: 2, want: "given twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWaiveFake()
			if c.break_ != nil {
				c.break_(f)
			}
			f.serve(t)
			args := c.args
			if args == nil {
				args = waiveArgs()
			}
			code, _, errb := try(t, args...)
			if code != c.code {
				t.Errorf("exit %d, want %d: %s", code, c.code, errb)
			}
			if f.merged() {
				t.Error("it merged")
			}
			if c.name != "the stamp cannot be posted" && len(f.stamps) != 0 {
				t.Errorf("it wrote before refusing: %q", f.stamps)
			}
			if !strings.Contains(errb, c.want) {
				t.Errorf("stderr does not say %q:\n%s", c.want, errb)
			}
		})
	}
}

// A merge that GitHub then refuses must not leave a stamp saying "Merged"
// uncorrected.
func TestWaiveCorrectsTheStampWhenTheMergeFails(t *testing.T) {
	f := newWaiveFake()
	f.mergeStatus = http.StatusMethodNotAllowed
	f.serve(t)
	if code, _, _ := try(t, waiveArgs()...); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(f.stamps) != 2 || !strings.Contains(f.stamps[1], "did NOT happen") {
		t.Errorf("stamps = %q", f.stamps)
	}
}

// Without --waive nothing about this path is asked: a cancelled check is red.
func TestACancelledCheckIsStillRedWithoutAWaiver(t *testing.T) {
	f := newWaiveFake()
	f.serve(t)
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 1 || f.merged() || !strings.Contains(errb, "test (macos-latest) cancelled") {
		t.Errorf("exit %d, merged %v:\n%s", code, f.merged(), errb)
	}
}

func TestQuoted(t *testing.T) {
	if got := quoted([]string{"a", "b"}); got != "'a', 'b'" {
		t.Errorf("quoted = %q", got)
	}
}
