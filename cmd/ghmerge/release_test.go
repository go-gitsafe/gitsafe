package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every test written before the release gate merges a pull request that the
// gate knows nothing about, and the fake GitHub of those tests refuses paths it
// has never heard of. They get a repository with no tags; the tests below say
// what they mean.
func init() {
	releaseFactsFor = func(string, string, *pr, int) (releaseFacts, error) { return releaseFacts{}, nil }
}

func withFacts(t *testing.T, f releaseFacts, err error) {
	t.Helper()
	was := releaseFactsFor
	releaseFactsFor = func(string, string, *pr, int) (releaseFacts, error) { return f, err }
	t.Cleanup(func() { releaseFactsFor = was })
}

func TestPublishesGo(t *testing.T) {
	for name, want := range map[string]bool{
		"main.go": true, "a/b/c.go": true, "go.mod": true, "sub/go.mod": true, "go.sum": true, "x/go.sum": true,
		"main_test.go": false, "a/b_test.go": false, "README.md": false, "go.mod.bak": false,
		"gogo.mod": false, "notgo.sum": false, "x.go.txt": false, "": false,
	} {
		if got := publishesGo(name); got != want {
			t.Errorf("publishesGo(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestHighestStableIsSemverNotListOrder(t *testing.T) {
	v, ok := highestStable([]string{"v0.9.0", "v0.10.0", "v0.2.0", "v0.10.0-rc.1", "v1.0.0-rc.1", "latest", "v01.0.0", "v0.9"})
	if !ok || v != [3]uint64{0, 10, 0} {
		t.Errorf("got %v %v, want v0.10.0", v, ok)
	}
	if _, ok := highestStable([]string{"v1.0.0-rc.1", "nightly"}); ok {
		t.Error("a pre-release and a non-semver name are not a release")
	}
	if _, ok := highestStable(nil); ok {
		t.Error("no tags is no release")
	}
	// The maximum must not depend on where in the list it sits.
	if v, _ := highestStable([]string{"v2.0.0", "v1.99.99"}); v != [3]uint64{2, 0, 0} {
		t.Errorf("got %v", v)
	}
	if v, _ := highestStable([]string{"v1.99.99", "v2.0.0"}); v != [3]uint64{2, 0, 0} {
		t.Errorf("got %v", v)
	}
	if v, _ := highestStable([]string{"v1.2.3", "v1.2.4", "v1.2.3"}); v != [3]uint64{1, 2, 4} {
		t.Errorf("got %v", v)
	}
	if v, _ := highestStable([]string{"v1.3.0", "v1.2.9"}); v != [3]uint64{1, 3, 0} {
		t.Errorf("got %v", v)
	}
}

// Too short is asserted on its own: with the length guard gone, a four-part
// name crashes the loop, and a crash is not the assertion.
func TestStableTooFewPartsIsRejected(t *testing.T) {
	for _, s := range []string{"v1", "v1.2", "v"} {
		if v, ok := stable(s); ok {
			t.Errorf("stable(%q) = %v, accepted", s, v)
		}
	}
}

func TestStableRejects(t *testing.T) {
	for _, s := range []string{"1.2.3", "v1.2", "v1.2.3.4", "v1..3", "v1.2.x", "v1.2.3-rc", "v1.2.3+b", "v0.00.1", "v1.2.1234567890", "v-1.2.3"} {
		if _, ok := stable(s); ok {
			t.Errorf("stable(%q) accepted", s)
		}
	}
	if v, ok := stable("v0.0.0"); !ok || v != [3]uint64{} {
		t.Errorf("v0.0.0: %v %v", v, ok)
	}
	if v, ok := stable("v10.20.30"); !ok || v != [3]uint64{10, 20, 30} {
		t.Errorf("v10.20.30: %v %v", v, ok)
	}
}

func TestReleaseRefusalBranches(t *testing.T) {
	tags := []string{"v0.9.0", "v0.10.0", "v0.2.0"}
	for name, c := range map[string]struct {
		f       releaseFacts
		refused bool
	}{
		"all three":      {releaseFacts{Tags: tags, GoMod: true, Triggers: []string{"a.go"}}, true},
		"no tag":         {releaseFacts{GoMod: true, Triggers: []string{"a.go"}}, false},
		"prerelease tag": {releaseFacts{Tags: []string{"v1.0.0-rc.1"}, GoMod: true, Triggers: []string{"a.go"}}, false},
		"no go.mod":      {releaseFacts{Tags: tags, Triggers: []string{"a.go"}}, false},
		"no go change":   {releaseFacts{Tags: tags, GoMod: true}, false},
	} {
		got := releaseRefusal("o/r", 7, c.f)
		if (got != "") != c.refused {
			t.Errorf("%s: refusal %q, want refused=%v", name, got, c.refused)
		}
	}
}

func TestReleaseRefusalNamesTheExactCommand(t *testing.T) {
	got := releaseRefusal("go-numerics/special", 106, releaseFacts{
		Tags: []string{"v0.9.0", "v0.22.0", "v0.3.1"}, GoMod: true, Triggers: []string{"go.mod", "x.go"},
	})
	for _, want := range []string{
		"ghrelease go-numerics/special 106 v0.22.1",
		"--no-release '<why>' go-numerics/special 106",
		"highest release v0.22.0",
		"go.mod, x.go",
		"https://go.dev/ref/mod#versions", "https://go.dev/ref/mod#vcs-version",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "more)") || strings.Contains(got, "and 0 more") {
		t.Errorf("claims files were left out:\n%s", got)
	}
}

func TestReleaseRefusalCapsTheFileList(t *testing.T) {
	five := releaseFacts{Tags: []string{"v1.0.0"}, GoMod: true, Triggers: []string{"a.go", "b.go", "c.go", "d.go", "e.go"}}
	if got := releaseRefusal("o/r", 1, five); strings.Contains(got, "more") || !strings.Contains(got, "e.go") {
		t.Errorf("five files are all shown:\n%s", got)
	}
	six := five
	six.Triggers = append(append([]string{}, five.Triggers...), "f.go", "g.go")
	got := releaseRefusal("o/r", 1, six)
	if !strings.Contains(got, "and 2 more") || strings.Contains(got, "f.go") || !strings.Contains(got, "e.go") {
		t.Errorf("seven files, five shown:\n%s", got)
	}
}

func TestGateRefusesBeforeMerging(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{checkrun(1, "test", "2024-01-01T00:00:00Z", "success")})
	withFacts(t, releaseFacts{Tags: []string{"v0.1.0"}, GoMod: true, Triggers: []string{"a.go"}}, nil)
	code, _, errb := try(t, "o/r", "5")
	if code != 1 || *merged {
		t.Errorf("exit %d, merged %v", code, *merged)
	}
	if !strings.Contains(errb, "ghrelease o/r 5 v0.1.1") {
		t.Errorf("the refusal does not name the command:\n%s", errb)
	}
}

func TestGateUnreadableRefusesWithTheCause(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{checkrun(1, "test", "2024-01-01T00:00:00Z", "success")})
	withFacts(t, releaseFacts{}, errors.New("listing the files of o/r#5: boom"))
	code, _, errb := try(t, "o/r", "5")
	if code != 1 || *merged {
		t.Errorf("exit %d, merged %v", code, *merged)
	}
	for _, want := range []string{"boom", "not \"no Go change\"", "--no-release"} {
		if !strings.Contains(errb, want) {
			t.Errorf("missing %q:\n%s", want, errb)
		}
	}
}

func TestNoReleaseMergesAndSaysWhy(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{checkrun(1, "test", "2024-01-01T00:00:00Z", "success")})
	// A gate that was still consulted would refuse here.
	withFacts(t, releaseFacts{}, errors.New("must not be asked"))
	code, out, errb := try(t, "--no-release", "series: v0.5.0 is cut by #9", "o/r", "5")
	if code != 0 || !*merged {
		t.Fatalf("exit %d, merged %v\n%s", code, *merged, errb)
	}
	if !strings.Contains(out, "o/r#5: --no-release: series: v0.5.0 is cut by #9") {
		t.Errorf("the reason is not printed:\n%s", out)
	}
}

func TestNoReleaseNeedsAReason(t *testing.T) {
	for _, reason := range []string{"", "   ", "\t"} {
		merged, _ := server(t, green(), nil)
		code, _, errb := try(t, "--no-release", reason, "o/r", "5")
		if code != 2 || *merged || !strings.Contains(errb, "--no-release needs a reason") {
			t.Errorf("reason %q: exit %d, merged %v\n%s", reason, code, *merged, errb)
		}
	}
}

func TestGateLetsAPlainPullRequestThrough(t *testing.T) {
	merged, _ := server(t, green(), []map[string]any{checkrun(1, "test", "2024-01-01T00:00:00Z", "success")})
	withFacts(t, releaseFacts{Tags: []string{"v0.1.0"}, GoMod: true}, nil)
	if code, _, errb := try(t, "o/r", "5"); code != 0 || !*merged {
		t.Errorf("exit %d, merged %v\n%s", code, *merged, errb)
	}
}

// ghGate answers the three reads the gate makes, and counts them.
type ghGate struct {
	t         *testing.T
	tags      []string
	tagsCode  int
	pages     [][]map[string]any // /pulls/N/files, one entry per page
	filesCode int
	tree      []string
	truncated bool
	treeCode  int
	asked     map[string]int
}

func (g *ghGate) serve(t *testing.T) {
	t.Helper()
	g.t = t
	g.asked = map[string]int{}
	s := httptest.NewServer(http.HandlerFunc(g.handler))
	t.Cleanup(s.Close)
	was := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = was })
}

func (g *ghGate) handler(w http.ResponseWriter, r *http.Request) {
	t := g.t
	{
		switch {
		case strings.Contains(r.URL.Path, "/git/matching-refs/tags/v"):
			g.asked["tags"]++
			if g.tagsCode != 0 {
				w.WriteHeader(g.tagsCode)
				return
			}
			var refs []map[string]any
			for _, n := range g.tags {
				refs = append(refs, map[string]any{"ref": "refs/tags/" + n})
			}
			_ = json.NewEncoder(w).Encode(refs)
		case strings.HasSuffix(r.URL.Path, "/files"):
			g.asked["files"]++
			if g.filesCode != 0 {
				w.WriteHeader(g.filesCode)
				return
			}
			if got := r.URL.Query().Get("per_page"); got != "100" {
				t.Errorf("per_page=%q", got)
			}
			var page int
			fmt.Sscan(r.URL.Query().Get("page"), &page)
			if page < 1 || page > len(g.pages) {
				_ = json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			_ = json.NewEncoder(w).Encode(g.pages[page-1])
		case strings.Contains(r.URL.Path, "/git/trees/"):
			g.asked["tree"]++
			if g.treeCode != 0 {
				w.WriteHeader(g.treeCode)
				return
			}
			if !strings.HasSuffix(r.URL.Path, "/abc") || r.URL.Query().Get("recursive") != "1" {
				t.Errorf("tree asked as %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			var es []map[string]any
			for _, p := range g.tree {
				es = append(es, map[string]any{"path": p})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"truncated": g.truncated, "tree": es})
		default:
			t.Errorf("the fake GitHub was asked for %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func files(names ...string) []map[string]any {
	var out []map[string]any
	for _, n := range names {
		out = append(out, map[string]any{"filename": n})
	}
	return out
}

func head() *pr {
	p := &pr{}
	p.Head.SHA = "abc"
	return p
}

func TestReadFactsNoTagReadsNothingElse(t *testing.T) {
	g := &ghGate{tags: []string{"v1.0.0-rc.1", "nightly"}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", head(), 5)
	if err != nil || releaseRefusal("o/r", 5, f) != "" {
		t.Fatalf("%v %+v", err, f)
	}
	if g.asked["files"] != 0 || g.asked["tree"] != 0 {
		t.Errorf("read on after the answer was known: %v", g.asked)
	}
}

func TestReadFactsGoChangeInATaggedModule(t *testing.T) {
	g := &ghGate{tags: []string{"v0.2.0", "v0.10.0", "v0.9.0"}, pages: [][]map[string]any{files("README.md", "pkg/a.go", "pkg/a_test.go", "go.sum")},
		tree: []string{"README.md", "sub/go.mod", "pkg/a.go"}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", head(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if !f.GoMod || strings.Join(f.Triggers, ",") != "go.sum,pkg/a.go" || len(f.Tags) != 3 {
		t.Errorf("%+v", f)
	}
	if got := releaseRefusal("o/r", 5, f); !strings.Contains(got, "ghrelease o/r 5 v0.10.1") {
		t.Errorf("%s", got)
	}
}

func TestReadFactsDocsOnlyReadsNoTree(t *testing.T) {
	g := &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{files("README.md", "x_test.go")}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", &pr{ChangedFiles: 2}, 5)
	if err != nil || len(f.Triggers) != 0 || g.asked["tree"] != 0 {
		t.Errorf("%v %+v %v", err, f, g.asked)
	}
}

func TestReadFactsNoGoModAnywhere(t *testing.T) {
	g := &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{files("a.go")}, tree: []string{"a.go", "docs/x.md", "mygo.mod"}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", head(), 5)
	if err != nil || f.GoMod || releaseRefusal("o/r", 5, f) != "" {
		t.Errorf("%v %+v", err, f)
	}
}

func TestReadFactsRenameOutOfGoCounts(t *testing.T) {
	g := &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{{{"filename": "old_test.go.txt", "previous_filename": "lib/old.go"}}}, tree: []string{"go.mod"}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", head(), 5)
	if err != nil || strings.Join(f.Triggers, ",") != "lib/old.go" {
		t.Errorf("%v %+v", err, f)
	}
}

func TestReadFactsPagesUntilAShortOne(t *testing.T) {
	var page1 []map[string]any
	for i := 0; i < 100; i++ {
		page1 = append(page1, map[string]any{"filename": fmt.Sprintf("doc%d.md", i)})
	}
	g := &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{page1, files("late.go")}, tree: []string{"go.mod"}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", &pr{ChangedFiles: 101, Head: head().Head}, 5)
	if err != nil || strings.Join(f.Triggers, ",") != "late.go" || g.asked["files"] != 2 {
		t.Errorf("%v %+v asked %v", err, f, g.asked)
	}
}

func TestReadFactsFullPageStopsAtTheCap(t *testing.T) {
	var page []map[string]any
	for i := 0; i < 100; i++ {
		page = append(page, map[string]any{"filename": fmt.Sprintf("doc%d.md", i)})
	}
	pages := make([][]map[string]any, 40)
	for i := range pages {
		pages[i] = page
	}
	g := &ghGate{tags: []string{"v0.1.0"}, pages: pages}
	g.serve(t)
	_, err := readReleaseFacts("tok", "o/r", &pr{ChangedFiles: 4000}, 5)
	if err == nil || g.asked["files"] != 30 {
		t.Errorf("err %v, asked %v", err, g.asked)
	}
}

func TestReadFactsAShortFileListIsUnreadable(t *testing.T) {
	g := &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{files("README.md")}}
	g.serve(t)
	_, err := readReleaseFacts("tok", "o/r", &pr{ChangedFiles: 3}, 5)
	if err == nil || !strings.Contains(err.Error(), "1 of its 3 changed files") {
		t.Errorf("%v", err)
	}
	// An EMPTY list for a pull request that has changes is the same refusal.
	g = &ghGate{tags: []string{"v0.1.0"}}
	g.serve(t)
	if _, err := readReleaseFacts("tok", "o/r", &pr{ChangedFiles: 1}, 5); err == nil {
		t.Error("an empty file list was read as no Go change")
	}
	// But an empty list for an empty pull request is just that.
	if _, err := readReleaseFacts("tok", "o/r", &pr{}, 5); err != nil {
		t.Errorf("%v", err)
	}
}

func TestReadFactsExactlyAsManyFilesAsItSays(t *testing.T) {
	g := &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{files("README.md", "b.md")}}
	g.serve(t)
	if _, err := readReleaseFacts("tok", "o/r", &pr{ChangedFiles: 2}, 5); err != nil {
		t.Errorf("a complete list refused: %v", err)
	}
}

func TestReadFactsTruncatedTree(t *testing.T) {
	g := &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{files("a.go")}, tree: []string{"a.go"}, truncated: true}
	g.serve(t)
	if _, err := readReleaseFacts("tok", "o/r", head(), 5); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("%v", err)
	}
	// A truncated tree that already shows a go.mod answers the question.
	g = &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{files("a.go")}, tree: []string{"go.mod"}, truncated: true}
	g.serve(t)
	if f, err := readReleaseFacts("tok", "o/r", head(), 5); err != nil || !f.GoMod {
		t.Errorf("%v %+v", err, f)
	}
}

func TestReadFactsEachReadFailsWithItsCause(t *testing.T) {
	for name, g := range map[string]*ghGate{
		"tags":  {tagsCode: 500},
		"files": {tags: []string{"v0.1.0"}, filesCode: 404},
		"tree":  {tags: []string{"v0.1.0"}, pages: [][]map[string]any{files("a.go")}, treeCode: 404},
	} {
		g.serve(t)
		_, err := readReleaseFacts("tok", "o/r", head(), 5)
		if err == nil || !strings.Contains(err.Error(), "listing the "+name) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The gate as a person meets it, through execute, against the fake GitHub.
func TestGateEndToEnd(t *testing.T) {
	g := &ghGate{tags: []string{"v0.3.0"}, pages: [][]map[string]any{files("cmd/x/main.go")}, tree: []string{"go.mod"}}
	was := releaseFactsFor
	releaseFactsFor = readReleaseFacts
	t.Cleanup(func() { releaseFactsFor = was })
	body := green()
	body["changed_files"] = 1
	body["head"].(map[string]any)["sha"] = "abc"
	// One server for everything: the gate's reads, then the merge it must not reach.
	merged := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge"):
			merged = true
		case strings.HasSuffix(r.URL.Path, "/files"), strings.Contains(r.URL.Path, "/git/"):
			g.handler(w, r)
		default:
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(s.Close)
	wasBase := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = wasBase })
	g.t, g.asked = t, map[string]int{}
	code, _, errb := try(t, "go-numerics/special", "106")
	if code != 1 || merged || !strings.Contains(errb, "ghrelease go-numerics/special 106 v0.3.1") {
		t.Errorf("exit %d merged %v:\n%s", code, merged, errb)
	}
}
