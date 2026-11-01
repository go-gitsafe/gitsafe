package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGitHub answers the calls the backfill makes: GraphQL (tags and
// releases of a repository), compare, create release, and a merge flow.
type fakeGitHub struct {
	tags      map[string][]tagInfo        // repo -> tags
	released  map[string]map[string]bool  // repo -> tag -> has a release
	created   map[string][]map[string]any // repo -> release bodies posted
	graphqlN  int
	restLeft  int // X-RateLimit-Remaining to report
	gqlLeft   int
	failState bool
}

func newFake() *fakeGitHub {
	return &fakeGitHub{tags: map[string][]tagInfo{}, released: map[string]map[string]bool{},
		created: map[string][]map[string]any{}, restLeft: 4000, gqlLeft: 4000}
}

func (f *fakeGitHub) do(req *http.Request) (*http.Response, error) {
	resp := func(code int, v any) (*http.Response, error) {
		b, _ := json.Marshal(v)
		h := http.Header{}
		h.Set("X-RateLimit-Remaining", fmt.Sprint(f.restLeft))
		return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: h, Body: io.NopCloser(bytes.NewReader(b))}, nil
	}
	p := req.URL.Path
	switch {
	case req.Method == "POST" && p == "/graphql":
		f.graphqlN++
		var q struct {
			Variables map[string]string
		}
		_ = json.NewDecoder(req.Body).Decode(&q)
		repo := q.Variables["o"] + "/" + q.Variables["n"]
		var refs, rels []map[string]any
		ts := f.tags[repo]
		for i := len(ts) - 1; i >= 0; i-- { // newest first
			refs = append(refs, map[string]any{"name": ts[i].Name,
				"target": map[string]any{"__typename": "Commit", "committedDate": ts[i].Date}})
		}
		for tag := range f.released[repo] {
			rels = append(rels, map[string]any{"tagName": tag, "createdAt": time.Now()})
		}
		return resp(200, map[string]any{"data": map[string]any{
			"repository": map[string]any{"refs": map[string]any{"nodes": refs}, "releases": map[string]any{"nodes": rels}},
			"rateLimit":  map[string]any{"remaining": f.gqlLeft}}})
	case req.Method == "GET" && strings.Contains(p, "/compare/"):
		return resp(200, map[string]any{"total_commits": 1,
			"commits": []map[string]any{{"sha": "abcdef1234567890", "commit": map[string]any{"message": "fix: a thing\n\nbody"}}}})
	case req.Method == "POST" && strings.HasSuffix(p, "/releases"):
		repo := strings.TrimSuffix(strings.TrimPrefix(p, "/repos/"), "/releases")
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		f.created[repo] = append(f.created[repo], body)
		if f.released[repo] == nil {
			f.released[repo] = map[string]bool{}
		}
		f.released[repo][body["tag_name"].(string)] = true
		f.restLeft--
		return resp(201, map[string]any{})
	case req.Method == "GET" && p == "/repos/o/r/actions/runs": // main CI: green
		return resp(200, map[string]any{"total_count": 1, "workflow_runs": []map[string]any{{"id": 1, "name": "ci",
			"status": "completed", "conclusion": "success", "head_sha": "cafebabe0000", "html_url": "https://example.test/run/1"}}})
	case req.Method == "GET" && strings.HasPrefix(p, "/repos/o/r/pulls/"):
		return resp(200, map[string]any{"merged": true, "merge_commit_sha": "cafebabe0000"})
	case req.Method == "POST" && strings.HasSuffix(p, "/git/refs"):
		return resp(201, map[string]any{})
	}
	return resp(404, map[string]any{})
}

func installFake(t *testing.T, f *fakeGitHub) {
	t.Helper()
	oh, om, on := httpDo, runMerge, now
	t.Cleanup(func() { httpDo, runMerge, now = oh, om, on })
	httpDo = f.do
	runMerge = func(string, int, io.Writer, io.Writer) error { return nil }
	now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
}

func day(d int) time.Time { return time.Date(2026, 10, d, 9, 0, 0, 0, time.UTC) }

// Releasing a new tag also releases the repository's older tags that had
// none: oldest first, never the Latest badge, bounded, and with notes that
// say they were backfilled. The new tag itself is not backfilled twice.
func TestAReleaseCatchesUpTheRepositorysOlderTags(t *testing.T) {
	f := newFake()
	f.tags["o/r"] = []tagInfo{{"v0.1.0", day(1)}, {"v0.2.0", day(5)}, {"v0.3.0", day(9)}, {"v0.4.0", day(26)}}
	f.released["o/r"] = map[string]bool{"v0.2.0": true}
	installFake(t, f)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-backfill-max", "1", "o/r", "3", "v0.4.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	got := f.created["o/r"]
	if len(got) != 2 {
		t.Fatalf("releases created %v", got)
	}
	if got[0]["tag_name"] != "v0.4.0" || got[0]["make_latest"] != "legacy" {
		t.Errorf("the new release first, Latest by semver: %v", got[0])
	}
	bf := got[1]
	if bf["tag_name"] != "v0.1.0" || bf["make_latest"] != "false" ||
		!strings.Contains(bf["body"].(string), "Release backfilled on 2026-10-26; tag created 2026-10-01") {
		t.Errorf("the oldest missing tag, never Latest, said as backfilled: %v", bf)
	}
	if !strings.Contains(out.String(), "1 more tag(s) without a release, left for a later run") {
		t.Errorf("the bound is not said: %s", out.String())
	}
}

func TestNoBackfillTurnsItOff(t *testing.T) {
	f := newFake()
	f.tags["o/r"] = []tagInfo{{"v0.1.0", day(1)}, {"v0.2.0", day(26)}}
	installFake(t, f)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-no-backfill", "o/r", "3", "v0.2.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if len(f.created["o/r"]) != 1 || f.graphqlN != 0 {
		t.Errorf("created %v, graphql calls %d", f.created["o/r"], f.graphqlN)
	}
}

func TestBackfillNotesListTheCommitsSinceThePreviousTag(t *testing.T) {
	f := newFake()
	installFake(t, f)
	st := repoState{tags: []tagInfo{{"v0.2.0", day(5)}, {"v0.1.0", day(1)}}, released: map[string]bool{}}
	notes, err := backfillNotes("t", "o/r", st, tagInfo{"v0.2.0", day(5)}, newBudget(0))
	if err != nil || !strings.Contains(notes, "Commits since v0.1.0") || !strings.Contains(notes, "- abcdef12 fix: a thing") {
		t.Errorf("%q %v", notes, err)
	}
	first, _ := backfillNotes("t", "o/r", st, tagInfo{"v0.1.0", day(1)}, newBudget(0))
	if !strings.Contains(first, "first tag") {
		t.Errorf("%q", first)
	}
}

func writeInventory(t *testing.T, repos ...string) string {
	t.Helper()
	var rs []map[string]any
	for _, r := range repos {
		org, name, _ := strings.Cut(r, "/")
		pushed := day(25)
		if name == "old" {
			pushed = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		}
		rs = append(rs, map[string]any{"org": org, "name": name, "archived": false, "pushed": pushed})
	}
	b, _ := json.Marshal(map[string]any{"taken": day(26), "repos": rs})
	p := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// `ghrelease backfill -since 14d`: repositories pushed within the window
// only, tags within the window only; the dry run creates nothing.
func TestBackfillSinceDryRunAndRun(t *testing.T) {
	f := newFake()
	f.tags["a/x"] = []tagInfo{{"v0.1.0", day(1)}, {"v0.2.0", day(20)}} // v0.1.0 is outside 14 days
	f.tags["a/y"] = []tagInfo{{"v1.0.0", day(15)}}
	f.released["a/y"] = map[string]bool{"v1.0.0": true}
	f.tags["a/old"] = []tagInfo{{"v0.1.0", day(20)}} // not pushed within the window: not even read
	installFake(t, f)
	inv := writeInventory(t, "a/x", "a/y", "a/old")
	tok := withToken(t, "t")
	var out, errb bytes.Buffer
	if code := run([]string{"backfill", "-since", "14d", "-inventory", inv, "-token-file", tok, "-dry-run"}, &out, &errb); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errb.String())
	}
	if len(f.created) != 0 || !strings.Contains(out.String(), "1 recent tag(s) without a release; would create 1") ||
		!strings.Contains(out.String(), "a/x: would release v0.2.0") || f.graphqlN != 2 {
		t.Errorf("dry run: graphql %d, created %v\n%s", f.graphqlN, f.created, out.String())
	}
	out.Reset()
	if code := run([]string{"backfill", "-since", "14d", "-inventory", inv, "-token-file", tok}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if len(f.created["a/x"]) != 1 || f.created["a/x"][0]["tag_name"] != "v0.2.0" || len(f.created["a/y"]) != 0 {
		t.Errorf("created %v", f.created)
	}
	if !strings.Contains(out.String(), "API calls: ") {
		t.Errorf("the calls are not counted: %s", out.String())
	}
}

// The budget is read from the answers; below the margin the run stops,
// says why, and exits 1 rather than spending what Renovate needs.
func TestBackfillStopsBelowTheMargin(t *testing.T) {
	f := newFake()
	f.gqlLeft = 500
	f.tags["a/x"] = []tagInfo{{"v0.2.0", day(20)}}
	f.tags["a/y"] = []tagInfo{{"v0.3.0", day(20)}}
	installFake(t, f)
	inv := writeInventory(t, "a/x", "a/y")
	var out, errb bytes.Buffer
	code := run([]string{"backfill", "-since", "14d", "-inventory", inv, "-token-file", withToken(t, "t"), "-margin", "1000"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "GraphQL budget is down to 500") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
	if len(f.created) != 0 || f.graphqlN != 1 {
		t.Errorf("it went on spending: %d graphql calls, created %v", f.graphqlN, f.created)
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"14d": 14 * 24 * time.Hour, "36h": 36 * time.Hour} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0d", "-3d", "xd", "soon"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
