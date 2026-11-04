package waiver

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func i64(n int64) *int64   { return &n }
func str(s string) *string { return &s }

func TestNeverStarted(t *testing.T) {
	ok := Job{Status: "completed", Conclusion: "cancelled"}
	cases := []struct {
		name string
		mod  func(j *Job)
		want string // "" = accepted
	}{
		{"no runner, no steps", func(j *Job) {}, ""},
		{"timed out unstarted", func(j *Job) { j.Conclusion = "timed_out" }, ""},
		{"failure unstarted", func(j *Job) { j.Conclusion = "failure" }, ""},
		{"runner id zero, queued steps", func(j *Job) {
			j.RunnerID = i64(0)
			j.RunnerName = str("")
			j.Steps = []Step{{Name: "Set up job", Status: "queued"}}
		}, ""},
		{"still running", func(j *Job) { j.Status = "in_progress" }, "not finished"},
		{"green", func(j *Job) { j.Conclusion = "success" }, "nothing to waive"},
		{"skipped", func(j *Job) { j.Conclusion = "skipped" }, "nothing to waive"},
		{"runner id", func(j *Job) { j.RunnerID = i64(5) }, "runner_id 5"},
		{"runner name", func(j *Job) { j.RunnerName = str("hosted-1") }, "hosted-1"},
		{"step with start time", func(j *Job) { j.Steps = []Step{{Name: "x", StartedAt: "2026-11-04T10:00:00Z"}} }, `step "x" started`},
		{"step succeeded", func(j *Job) { j.Steps = []Step{{Name: "y", Conclusion: "success"}} }, `step "y" started`},
		{"step failed", func(j *Job) { j.Steps = []Step{{Name: "z", Conclusion: "failure"}} }, `step "z" started`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := ok
			c.mod(&j)
			got, why := NeverStarted(j)
			if (c.want == "") != got || !strings.Contains(why, c.want) {
				t.Errorf("NeverStarted = %v, %q; want %q", got, why, c.want)
			}
		})
	}
}

func TestParseEvidence(t *testing.T) {
	good := []string{
		"https://github.com/o/r/pull/3#issuecomment-42",
		"https://github.com/o/r/issues/3#issuecomment-42",
		"  https://GitHub.com/O/R/pull/3#issuecomment-42 ",
	}
	for _, g := range good {
		if id, err := ParseEvidence(g, "o/r", 3); err != nil || id != 42 {
			t.Errorf("%q: %d, %v", g, id, err)
		}
	}
	for _, b := range []string{
		"", "http://github.com/o/r/pull/3#issuecomment-42", "https://example.com/o/r/pull/3#issuecomment-42",
		"https://github.com/o/r/pull/4#issuecomment-42", "https://github.com/o/other/pull/3#issuecomment-42",
		"https://github.com/o/r/pull/3", "https://github.com/o/r/pull/3#issuecomment-x",
		"https://github.com/o/r/pull/3#issuecomment-0", "https://github.com/o/r/pull/3#discussion_r1", "://bad",
	} {
		if _, err := ParseEvidence(b, "o/r", 3); err == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

func getter(answers map[string]any, fail map[string]error) Getter {
	return func(u string, into any) error {
		for suffix, err := range fail {
			if strings.HasSuffix(u, suffix) {
				return err
			}
		}
		for suffix, a := range answers {
			if strings.HasSuffix(u, suffix) {
				b, _ := json.Marshal(a)
				return json.Unmarshal(b, into)
			}
		}
		return errors.New("404 " + u)
	}
}

func TestCheckEvidence(t *testing.T) {
	comment := func(login, body, issue string) map[string]any {
		return map[string]any{"body": body, "issue_url": issue, "user": map[string]any{"login": login}}
	}
	const on3 = "https://api.github.com/repos/o/r/issues/3"
	cases := []struct {
		name    string
		sha     string
		answers map[string]any
		fail    map[string]error
		want    string
	}{
		{"ok", sha, map[string]any{"/user": map[string]any{"login": "me"}, "/comments/9": comment("ME", "at "+strings.ToUpper(sha), on3)}, nil, ""},
		{"short sha", "abc", nil, nil, "not a full 40-hex"},
		{"user unreadable", sha, nil, map[string]error{"/user": errors.New("boom")}, "who the token belongs to"},
		{"user empty", sha, map[string]any{"/user": map[string]any{"login": ""}}, nil, "did not say who"},
		{"comment unreadable", sha, map[string]any{"/user": map[string]any{"login": "me"}}, nil, "reading the evidence comment"},
		{"other pull request", sha, map[string]any{"/user": map[string]any{"login": "me"}, "/comments/9": comment("me", sha, "https://api.github.com/repos/o/r/issues/33")}, nil, "is not on o/r#3"},
		{"other author", sha, map[string]any{"/user": map[string]any{"login": "me"}, "/comments/9": comment("you", sha, on3)}, nil, "not by the authenticated user"},
		{"no sha", sha, map[string]any{"/user": map[string]any{"login": "me"}, "/comments/9": comment("me", "fine", on3)}, nil, "does not contain the full SHA"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckEvidence(getter(c.answers, c.fail), "https://api", "o/r", 3, 9, c.sha)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestIsFullSHA(t *testing.T) {
	if !IsFullSHA(sha) || IsFullSHA(sha[:39]) || IsFullSHA(strings.ToUpper(sha)) || IsFullSHA(sha+"0") {
		t.Error("IsFullSHA wrong")
	}
}

func TestFetchJobAndRunJobs(t *testing.T) {
	g := getter(map[string]any{
		"/actions/jobs/5":                   map[string]any{"id": 5, "status": "completed", "runner_id": nil},
		"/actions/runs/7/jobs?per_page=100": map[string]any{"total_count": 1, "jobs": []any{map[string]any{"id": 5}}},
		"/actions/runs/8/jobs?per_page=100": map[string]any{"total_count": 3, "jobs": []any{map[string]any{"id": 5}}},
	}, nil)
	if j, err := FetchJob(g, "https://api", "o/r", 5); err != nil || j.ID != 5 || j.RunnerID != nil {
		t.Errorf("FetchJob = %+v, %v", j, err)
	}
	if _, err := FetchJob(g, "https://api", "o/r", 6); err == nil {
		t.Error("a missing job was read")
	}
	if jobs, err := RunJobs(g, "https://api", "o/r", 7); err != nil || len(jobs) != 1 {
		t.Errorf("RunJobs = %v, %v", jobs, err)
	}
	if _, err := RunJobs(g, "https://api", "o/r", 8); err == nil || !strings.Contains(err.Error(), "only 1 were read") {
		t.Errorf("a truncated list was accepted: %v", err)
	}
	if _, err := RunJobs(g, "https://api", "o/r", 9); err == nil {
		t.Error("a missing run was read")
	}
}

func TestNamesAndStamp(t *testing.T) {
	if err := Names([]string{"a", "b"}); err != nil {
		t.Error(err)
	}
	if Names([]string{""}) == nil || Names([]string{"a", "a"}) == nil {
		t.Error("bad names accepted")
	}
	want := "Merged with 'a', 'b' waived: no runner acquired; evidence U (local run at S)"
	if got := Stamp("Merged", []string{"a", "b"}, "U", "S"); got != want {
		t.Errorf("Stamp = %q", got)
	}
}
