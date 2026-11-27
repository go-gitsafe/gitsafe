package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/relver"
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

func TestOwnerOfIsTheNearestGoMod(t *testing.T) {
	mods := []string{"", "sub", "sub/deep", "other"}
	for file, want := range map[string]string{
		"main.go": "", "pkg/a.go": "", "go.mod": "",
		"sub/a.go": "sub", "sub/go.mod": "sub", "sub/x/y.go": "sub",
		"sub/deep/a.go": "sub/deep", "sub/deep/go.sum": "sub/deep",
		"subway/a.go": "", // sub is a directory, not a name prefix
		"other/a.go":  "other",
	} {
		if got, ok := ownerOf(file, mods); !ok || got != want {
			t.Errorf("ownerOf(%q) = %q %v, want %q", file, got, ok, want)
		}
	}
	// No go.mod above a file: it belongs to no module.
	if got, ok := ownerOf("a.go", []string{"sub"}); ok {
		t.Errorf("a file under no go.mod has the owner %q", got)
	}
	if _, ok := ownerOf("a.go", nil); ok {
		t.Error("a repository with no go.mod owns no file")
	}
	// Order of the list does not matter.
	if got, _ := ownerOf("sub/deep/a.go", []string{"sub/deep", "", "sub"}); got != "sub/deep" {
		t.Errorf("got %q", got)
	}
}

func TestModuleLine(t *testing.T) {
	for dir, want := range map[string]struct {
		prefix string
		major  uint64
	}{
		"":         {"", 0},
		"sub":      {"sub/", 0},
		"a/b":      {"a/b/", 0},
		"sub/v2":   {"sub/", 2},
		"a/b/v10":  {"a/b/", 10},
		"v2":       {"", 2}, // a root repository's major version subdirectory
		"sub/v1":   {"sub/v1/", 0},
		"sub/v02":  {"sub/v02/", 0},
		"sub/v2x":  {"sub/v2x/", 0},
		"sub/vv2":  {"sub/vv2/", 0},
		"sub/v":    {"sub/v/", 0},
		"v1234567": {"", 1234567},
		// Nine digits is the longest accepted, ten is a directory name.
		"v123456789":  {"", 123456789},
		"v1234567890": {"v1234567890/", 0},
	} {
		if p, m := moduleLine(dir); p != want.prefix || m != want.major {
			t.Errorf("moduleLine(%q) = %q %d, want %q %d", dir, p, m, want.prefix, want.major)
		}
	}
}

func TestHighestOfIsPerModuleAndSemverNotListOrder(t *testing.T) {
	tags := []string{"v0.9.0", "v0.10.0", "sub/v0.3.0", "sub/v0.12.0", "sub/v1.0.0-rc.1", "sub/v2.0.0", "sub/v2.1.0", "sub/deep/v9.0.0", "latest"}
	for name, c := range map[string]struct {
		prefix string
		major  uint64
		want   relver.Version
		found  bool
	}{
		"root":              {"", 0, relver.Version{0, 10, 0}, true},
		"sub":               {"sub/", 0, relver.Version{2, 1, 0}, true},
		"sub major 2 only":  {"sub/", 2, relver.Version{2, 1, 0}, true},
		"sub major 1 never": {"sub/", 1, relver.Version{}, false}, // only a pre-release
		"deep":              {"sub/deep/", 0, relver.Version{9, 0, 0}, true},
		"untagged":          {"none/", 0, relver.Version{}, false},
	} {
		got, found := highestOf(tags, c.prefix, c.major)
		if found != c.found || got != c.want {
			t.Errorf("%s: %v %v, want %v %v", name, got, found, c.want, c.found)
		}
	}
}

func TestAnyStable(t *testing.T) {
	for name, c := range map[string]struct {
		tags []string
		want bool
	}{
		"none":                {nil, false},
		"pre-release only":    {[]string{"v1.0.0-rc.1", "sub/v2.0.0-beta"}, false},
		"not semver":          {[]string{"nightly", "sub/latest", "release/2024"}, false},
		"root":                {[]string{"v0.1.0"}, true},
		"nested only":         {[]string{"sub/v0.1.0", "nightly"}, true},
		"a malformed prefix":  {[]string{"a//v1.0.0", "../v1.0.0"}, false},
		"nested beside junk":  {[]string{"junk", "a/b/v1.2.3"}, true},
		"leading zero is not": {[]string{"sub/v01.0.0"}, false},
	} {
		if got := anyStable(c.tags); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestReleaseRefusalBranches(t *testing.T) {
	tags := []string{"v0.9.0", "v0.10.0", "v0.2.0"}
	root := []string{""}
	for name, c := range map[string]struct {
		f       releaseFacts
		refused bool
	}{
		"all three":      {releaseFacts{Tags: tags, Modules: root, Triggers: []string{"a.go"}}, true},
		"no tag":         {releaseFacts{Modules: root, Triggers: []string{"a.go"}}, false},
		"prerelease tag": {releaseFacts{Tags: []string{"v1.0.0-rc.1"}, Modules: root, Triggers: []string{"a.go"}}, false},
		"no go.mod":      {releaseFacts{Tags: tags, Triggers: []string{"a.go"}}, false},
		"no go change":   {releaseFacts{Tags: tags, Modules: root}, false},
		// The gap this closes: tags that all belong to a nested module.
		"nested tags, root change":      {releaseFacts{Tags: []string{"sub/v1.0.0"}, Modules: []string{"", "sub"}, Triggers: []string{"a.go"}}, false},
		"nested tags, nested change":    {releaseFacts{Tags: []string{"sub/v1.0.0"}, Modules: []string{"", "sub"}, Triggers: []string{"sub/a.go"}}, true},
		"nested tags, nested go.mod":    {releaseFacts{Tags: []string{"sub/v1.0.0"}, Modules: []string{"sub"}, Triggers: []string{"sub/go.mod"}}, true},
		"root tags, nested change":      {releaseFacts{Tags: tags, Modules: []string{"", "sub"}, Triggers: []string{"sub/a.go"}}, false},
		"nested module never tagged":    {releaseFacts{Tags: []string{"sub/v1.0.0"}, Modules: []string{"sub", "other"}, Triggers: []string{"other/a.go"}}, false},
		"file under no go.mod":          {releaseFacts{Tags: tags, Modules: []string{"sub"}, Triggers: []string{"a.go"}}, false},
		"nested tag is pre-release":     {releaseFacts{Tags: []string{"sub/v1.0.0-rc.1"}, Modules: []string{"sub"}, Triggers: []string{"sub/a.go"}}, false},
		"deeper module shadows nested":  {releaseFacts{Tags: []string{"sub/v1.0.0"}, Modules: []string{"sub", "sub/deep"}, Triggers: []string{"sub/deep/a.go"}}, false},
		"major subdirectory is sub":     {releaseFacts{Tags: []string{"sub/v2.0.0"}, Modules: []string{"sub/v2"}, Triggers: []string{"sub/v2/a.go"}}, true},
		"major subdirectory, only v1":   {releaseFacts{Tags: []string{"sub/v1.0.0"}, Modules: []string{"sub/v2"}, Triggers: []string{"sub/v2/a.go"}}, false},
		"root v2 directory is the root": {releaseFacts{Tags: []string{"v2.0.0"}, Modules: []string{"v2"}, Triggers: []string{"v2/a.go"}}, true},
	} {
		got := releaseRefusal("o/r", 7, c.f)
		if (got != "") != c.refused {
			t.Errorf("%s: refusal %q, want refused=%v", name, got, c.refused)
		}
	}
}

func TestReleaseRefusalNamesTheExactCommand(t *testing.T) {
	got := releaseRefusal("go-numerics/special", 106, releaseFacts{
		Tags: []string{"v0.9.0", "v0.22.0", "v0.3.1"}, Modules: []string{""}, Triggers: []string{"go.mod", "x.go"},
	})
	for _, want := range []string{
		"ghrelease go-numerics/special 106 v0.22.1",
		"--no-release '<why>' go-numerics/special 106",
		"highest release v0.22.0",
		"the root module",
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

// The refusal of a nested module names the PREFIXED tag, the next PATCH of that
// module's own line (not the root's), and a command ghrelease accepts.
func TestReleaseRefusalNamesTheNestedModulesTag(t *testing.T) {
	got := releaseRefusal("o/r", 9, releaseFacts{
		Tags:     []string{"v5.0.0", "sub/v0.2.0", "sub/v0.10.0", "sub/v0.9.0", "other/v7.0.0"},
		Modules:  []string{"", "sub", "other"},
		Triggers: []string{"sub/a.go"},
	})
	for _, want := range []string{
		"ghrelease o/r 9 sub/v0.10.1",
		"module sub (highest release sub/v0.10.0)",
		"sub/a.go",
		"--no-release '<why>' o/r 9",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"v5.0.1", "other/", "the root module", "once merged"} {
		if strings.Contains(got, bad) {
			t.Errorf("names %q, which is not what this pull request changes:\n%s", bad, got)
		}
	}
}

func TestReleaseRefusalNamesTheMajorSubdirectoryTag(t *testing.T) {
	got := releaseRefusal("o/r", 3, releaseFacts{
		Tags: []string{"sub/v2.0.0", "sub/v2.3.0", "sub/v1.9.0"}, Modules: []string{"sub", "sub/v2"}, Triggers: []string{"sub/v2/a.go"},
	})
	if !strings.Contains(got, "ghrelease o/r 3 sub/v2.3.1") || !strings.Contains(got, "module sub/v2 (highest release sub/v2.3.0)") {
		t.Errorf("%s", got)
	}
}

// ghrelease tags one module per merge: a second module is a second command,
// after the merge, which ghrelease runs against the merged pull request.
func TestReleaseRefusalWithSeveralModules(t *testing.T) {
	got := releaseRefusal("o/r", 4, releaseFacts{
		Tags:     []string{"v1.0.0", "sub/v0.1.0", "z/v3.0.0"},
		Modules:  []string{"", "sub", "z"},
		Triggers: []string{"a.go", "sub/b.go", "z/c.go"},
	})
	first := strings.Index(got, "merge AND tag it in one command: ghrelease o/r 4 v1.0.1")
	second := strings.Index(got, "module sub, once merged")
	third := strings.Index(got, "module z, once merged")
	if first < 0 || second < first || third < second {
		t.Errorf("the commands are missing or out of order:\n%s", got)
	}
	for _, want := range []string{"ghrelease o/r 4 sub/v0.1.1", "ghrelease o/r 4 z/v3.0.1", "the root module (highest release v1.0.0)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// An untagged module is not mentioned at all.
	got = releaseRefusal("o/r", 4, releaseFacts{
		Tags: []string{"v1.0.0"}, Modules: []string{"", "sub"}, Triggers: []string{"a.go", "sub/b.go"},
	})
	if strings.Contains(got, "sub") || strings.Contains(got, "once merged") {
		t.Errorf("an untagged module is named:\n%s", got)
	}
}

// A tag ghrelease would refuse to create (its shape check) is said to be one,
// rather than offered as a command that will fail after the merge decision.
func TestReleaseRefusalSaysWhenGhreleaseCannotTagTheModule(t *testing.T) {
	got := releaseRefusal("o/r", 8, releaseFacts{
		Tags: []string{"v1.0.0", "my dir/v0.1.0"}, Modules: []string{"my dir"}, Triggers: []string{"my dir/a.go"},
	})
	for _, want := range []string{"module my dir: ghrelease cannot tag it", `"my dir/v0.1.1"`, "--no-release '<why>' o/r 8"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ghrelease o/r 8 ") {
		t.Errorf("offers a command that cannot work:\n%s", got)
	}
}

func TestReleaseRefusalCapsTheFileList(t *testing.T) {
	five := releaseFacts{Tags: []string{"v1.0.0"}, Modules: []string{""}, Triggers: []string{"a.go", "b.go", "c.go", "d.go", "e.go"}}
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
	withFacts(t, releaseFacts{Tags: []string{"v0.1.0"}, Modules: []string{""}, Triggers: []string{"a.go"}}, nil)
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
	withFacts(t, releaseFacts{Tags: []string{"v0.1.0"}, Modules: []string{""}}, nil)
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
		case strings.HasSuffix(r.URL.Path, "/git/matching-refs/tags"):
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
		tree: []string{"README.md", "go.mod", "sub/go.mod", "pkg/a.go"}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", head(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.Modules, ",") != ",sub" || strings.Join(f.Triggers, ",") != "go.sum,pkg/a.go" || len(f.Tags) != 3 {
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
	if err != nil || len(f.Modules) != 0 || releaseRefusal("o/r", 5, f) != "" {
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
	// Nor does a go.mod already seen answer it: a nested go.mod missing from what
	// was sent would hand its files to the module above.
	g = &ghGate{tags: []string{"v0.1.0"}, pages: [][]map[string]any{files("sub/a.go")}, tree: []string{"go.mod"}, truncated: true}
	g.serve(t)
	if _, err := readReleaseFacts("tok", "o/r", head(), 5); err == nil || !strings.Contains(err.Error(), "which go.mod owns") {
		t.Errorf("%v", err)
	}
}

// The tags of a nested module are not under tags/v: the list asked for is every
// tag, and a repository whose only tags are nested ones is a tagged repository.
func TestReadFactsOnlyNestedTags(t *testing.T) {
	g := &ghGate{tags: []string{"sub/v0.4.0", "sub/v0.3.0"}, pages: [][]map[string]any{files("sub/a.go", "b.go")},
		tree: []string{"go.mod", "sub/go.mod", "sub/a.go", "b.go"}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", head(), 5)
	if err != nil {
		t.Fatal(err)
	}
	got := releaseRefusal("o/r", 5, f)
	if !strings.Contains(got, "ghrelease o/r 5 sub/v0.4.1") || strings.Contains(got, "b.go") || strings.Contains(got, "the root module") {
		t.Errorf("%s", got)
	}
	if g.asked["tags"] != 1 {
		t.Errorf("asked %v", g.asked)
	}
	// The same pull request, the nested module changed alone in a repository
	// whose root has a go.mod but no tags: still the nested module's tag.
	g = &ghGate{tags: []string{"sub/v1.0.0"}, pages: [][]map[string]any{files("sub/go.sum")}, tree: []string{"go.mod", "sub/go.mod"}}
	g.serve(t)
	f, err = readReleaseFacts("tok", "o/r", head(), 5)
	if err != nil || !strings.Contains(releaseRefusal("o/r", 5, f), "ghrelease o/r 5 sub/v1.0.1") {
		t.Errorf("%v %+v", err, f)
	}
}

// Tags of another module do not make this one a tagged module.
func TestReadFactsChangeOutsideTheTaggedModule(t *testing.T) {
	g := &ghGate{tags: []string{"sub/v1.0.0"}, pages: [][]map[string]any{files("pkg/a.go")}, tree: []string{"go.mod", "sub/go.mod", "pkg/a.go"}}
	g.serve(t)
	f, err := readReleaseFacts("tok", "o/r", head(), 5)
	if err != nil || releaseRefusal("o/r", 5, f) != "" {
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
