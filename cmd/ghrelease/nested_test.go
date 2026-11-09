package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// contentsReply is GitHub's "Get repository content" answer for a file.
func contentsReply(text string) *http.Response {
	b, _ := json.Marshal(map[string]string{
		"type": "file", "encoding": "base64",
		"content": base64.StdEncoding.EncodeToString([]byte(text)),
	})
	return reply(http.StatusOK, string(b))
}

// nestedRun runs ghrelease for o/r#3 as tag. files maps a repository path to its
// content at the merge commit; tags maps a matching-refs prefix to the tags it
// lists.
func nestedRun(t *testing.T, tag string, files map[string]string, tags map[string][]string, args ...string) (int, *stub, string, string) {
	t.Helper()
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3":   reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000","base":{"ref":"main"}}`),
		"POST /repos/o/r/git/refs": reply(http.StatusCreated, `{}`),
		"POST /repos/o/r/releases": reply(http.StatusCreated, `{}`),
	}}
	for p, text := range files {
		s.byPath["GET /repos/o/r/contents/"+p] = contentsReply(text)
	}
	for prefix, have := range tags {
		s.byPath["GET /repos/o/r/git/matching-refs/tags/"+prefix] = reply(http.StatusOK, existing(have...))
	}
	install(t, s)
	var out, errb bytes.Buffer
	full := append([]string{"-token-file", withToken(t, "t"), "-no-backfill"}, args...)
	code := run(append(full, "o/r", "3", tag), &out, &errb)
	return code, s, out.String(), errb.String()
}

func asked(s *stub, call string) bool {
	for _, c := range s.calls {
		if c == call {
			return true
		}
	}
	return false
}

// The accepted and the refused tags of a nested module, judged at the merge
// commit (go.dev/ref/mod#vcs-version).
func TestNestedTagIsJudgedAgainstTheGoModAtTheMergeCommit(t *testing.T) {
	for _, c := range []struct {
		name  string
		tag   string
		files map[string]string
		want  string // "" = accepted; else a substring of the refusal
	}{
		{"v0 module", "media/v0.2.0", map[string]string{"media/go.mod": "module github.com/o/r/media\n\ngo 1.27\n"}, ""},
		{"v1 module", "media/v1.0.0", map[string]string{"media/go.mod": "module github.com/o/r/media\n"}, ""},
		{"deep directory", "cmd/wctr/v0.1.0", map[string]string{"cmd/wctr/go.mod": "module github.com/o/r/cmd/wctr\n"}, ""},
		{"quoted path with a comment", "media/v0.2.0", map[string]string{"media/go.mod": "// c\nmodule \"github.com/o/r/media\" // x\n"}, ""},
		{"module block", "media/v0.2.0", map[string]string{"media/go.mod": "module (\n\tgithub.com/o/r/media\n)\n"}, ""},
		{"v2 module, suffix in the path, go.mod in dir", "media/v2.0.0", map[string]string{"media/go.mod": "module github.com/o/r/media/v2\n"}, ""},
		{"v2 module in a major version subdirectory", "media/v2.0.0", map[string]string{
			"media/go.mod":    "module github.com/o/r/media\n",
			"media/v2/go.mod": "module github.com/o/r/media/v2\n"}, ""},

		{"no go.mod there", "media/v0.2.0", nil, "no go.mod at media/go.mod"},
		{"a go.mod of another directory", "media/v0.2.0", map[string]string{"other/go.mod": "module github.com/o/r/media\n"}, "no go.mod at media/go.mod"},
		{"path of the root module", "media/v0.2.0", map[string]string{"media/go.mod": "module github.com/o/r\n"}, `module "github.com/o/r", which does not end in /media`},
		{"path of another directory", "media/v0.2.0", map[string]string{"media/go.mod": "module github.com/o/r/audio\n"}, "does not end in /media"},
		{"prefix is not a whole element", "media/v0.2.0", map[string]string{"media/go.mod": "module github.com/o/r/xmedia\n"}, "does not end in /media"},
		{"v1 suffix is not a valid path", "media/v1.0.0", map[string]string{"media/go.mod": "module github.com/o/r/media/v1\n"}, "does not end in /media"},
		{"v2 tag, v1 path", "media/v2.0.0", map[string]string{"media/go.mod": "module github.com/o/r/media\n"}, "no /vN major version suffix"},
		{"v3 tag, v2 path", "media/v3.0.0", map[string]string{"media/go.mod": "module github.com/o/r/media/v2\n"}, "major version 2) but the tag's major version is 3"},
		{"v1 tag, v2 path", "media/v1.4.0", map[string]string{"media/go.mod": "module github.com/o/r/media/v2\n"}, "major version 2) but the tag's major version is 1"},
		{"v2 suffix on another directory", "media/v2.0.0", map[string]string{"media/go.mod": "module github.com/o/r/audio/v2\n"}, "does not end in /media/v2"},
		{"go.mod with no module line", "media/v0.2.0", map[string]string{"media/go.mod": "go 1.27\n"}, "has no module directive"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, s, _, errs := nestedRun(t, c.tag, c.files, nil)
			if c.want == "" {
				if code != 0 || !tagged(s) {
					t.Fatalf("exit %d, tagged %v: %s", code, tagged(s), errs)
				}
				if !strings.Contains(s.bodies["POST /repos/o/r/git/refs"], `"ref":"refs/tags/`+c.tag+`"`) {
					t.Errorf("tag request %q", s.bodies["POST /repos/o/r/git/refs"])
				}
				return
			}
			if code != 1 || tagged(s) || s.bodies["POST /repos/o/r/releases"] != "" {
				t.Fatalf("exit %d, tagged %v (want a refusal, nothing tagged): %s", code, tagged(s), errs)
			}
			for _, w := range []string{c.want, "nothing was tagged", "cafebabe"} {
				if !strings.Contains(errs, w) {
					t.Errorf("stderr lacks %q: %q", w, errs)
				}
			}
		})
	}
}

// A go.mod that cannot be read is not "no go.mod": nothing is tagged and the
// reason is the read's.
func TestNestedGoModReadFailureIsNotAnAbsence(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3":               reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000","base":{"ref":"main"}}`),
		"GET /repos/o/r/contents/media/go.mod": reply(http.StatusInternalServerError, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-no-backfill", "o/r", "3", "media/v0.2.0"}, &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if tagged(s) || !strings.Contains(errb.String(), "could not read media/go.mod") || strings.Contains(errb.String(), "no go.mod at") {
		t.Errorf("tagged %v, stderr %q", tagged(s), errb.String())
	}
}

// A directory (or anything not a file) at <dir>/go.mod is not a go.mod.
func TestNestedGoModThatIsADirectoryIsAbsent(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3":               reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000","base":{"ref":"main"}}`),
		"GET /repos/o/r/contents/media/go.mod": reply(http.StatusOK, `{"type":"dir"}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	run([]string{"-token-file", withToken(t, "t"), "-no-backfill", "o/r", "3", "media/v0.2.0"}, &out, &errb)
	if tagged(s) || !strings.Contains(errb.String(), "no go.mod at media/go.mod") {
		t.Errorf("tagged %v, stderr %q", tagged(s), errb.String())
	}
}

// Ordering is among the <dir>/v* tags ONLY.
func TestNestedOrderingIsPerPrefix(t *testing.T) {
	gomod := map[string]string{"media/go.mod": "module github.com/o/r/media\n"}
	root := []string{"v0.9.0", "v1.5.0"}
	media := []string{"media/v0.1.0", "media/v0.2.0"}
	for _, c := range []struct {
		name string
		tag  string
		tags map[string][]string
		ok   bool
	}{
		{"above media's highest, below the root's", "media/v0.3.0", map[string][]string{"v": root, "media/v": media}, true},
		{"first tag of the module, root has many", "media/v0.1.0", map[string][]string{"v": root, "media/v": nil}, true},
		{"below media's highest", "media/v0.1.5", map[string][]string{"v": root, "media/v": media}, false},
		{"equal to media's highest", "media/v0.2.0", map[string][]string{"v": root, "media/v": media}, false},
		{"another module's tags do not count", "media/v0.1.0", map[string][]string{"media/v": nil, "audio/v": {"audio/v3.0.0"}}, true},
		{"deeper directory's tags do not count", "media/v0.2.0", map[string][]string{"media/v": {"media/v0.1.0", "media/v/9.9.9", "media/v9/x/v9.0.0"}}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, s, _, errs := nestedRun(t, c.tag, gomod, c.tags)
			if (code == 0) != c.ok || tagged(s) != c.ok {
				t.Fatalf("exit %d, tagged %v, want ok=%v: %s", code, tagged(s), c.ok, errs)
			}
			if !c.ok {
				for _, w := range []string{"media/v0.2.0", "media/v* tags", "PATCH media/v0.2.1", "MINOR media/v0.3.0", "MAJOR media/v1.0.0"} {
					if !strings.Contains(errs, w) {
						t.Errorf("stderr lacks %q: %q", w, errs)
					}
				}
			}
			// The root module's tags are never listed for a nested tag.
			if asked(s, "GET /repos/o/r/git/matching-refs/tags/v") {
				t.Error("it listed the root module's tags to order a nested tag")
			}
		})
	}
}

// And the other way: a root tag is not ordered against nested tags.
func TestRootOrderingIgnoresNestedTags(t *testing.T) {
	code, s, _, errs := nestedRun(t, "v0.1.0", nil, map[string][]string{"v": nil, "media/v": {"media/v7.0.0"}})
	if code != 0 || !tagged(s) {
		t.Fatalf("exit %d, tagged %v: %s", code, tagged(s), errs)
	}
	if asked(s, "GET /repos/o/r/contents/media/go.mod") {
		t.Error("a root tag read a go.mod")
	}
}

// The release of a nested module is not Latest unless --make-latest, and its
// notes are cut against the module's own previous tag.
func TestNestedReleaseIsNotLatestAndHasItsOwnPreviousTag(t *testing.T) {
	gomod := map[string]string{"media/go.mod": "module github.com/o/r/media\n"}
	tags := map[string][]string{"v": {"v5.0.0"}, "media/v": {"media/v0.1.0"}}

	_, s, out, errs := nestedRun(t, "media/v0.2.0", gomod, tags)
	rel := s.bodies["POST /repos/o/r/releases"]
	for _, w := range []string{`"tag_name":"media/v0.2.0"`, `"make_latest":"false"`, `"previous_tag_name":"media/v0.1.0"`, `"generate_release_notes":true`} {
		if !strings.Contains(rel, w) {
			t.Errorf("release lacks %s: %q (%s)", w, rel, errs)
		}
	}
	if !strings.Contains(out, "released media/v0.2.0") {
		t.Errorf("stdout %q", out)
	}

	_, s, _, errs = nestedRun(t, "media/v0.2.0", gomod, tags, "--make-latest")
	if rel := s.bodies["POST /repos/o/r/releases"]; !strings.Contains(rel, `"make_latest":"legacy"`) {
		t.Errorf("--make-latest: release %q (%s)", rel, errs)
	}

	// The first tag of a module has no previous one to name.
	_, s, _, _ = nestedRun(t, "media/v0.1.0", gomod, map[string][]string{"v": {"v5.0.0"}})
	if rel := s.bodies["POST /repos/o/r/releases"]; strings.Contains(rel, "previous_tag_name") {
		t.Errorf("a first tag named a previous one: %q", rel)
	}

	// A root release is unchanged: Latest by GitHub's rule, no previous_tag_name.
	_, s, _, _ = nestedRun(t, "v5.1.0", nil, map[string][]string{"v": {"v5.0.0"}})
	if rel := s.bodies["POST /repos/o/r/releases"]; !strings.Contains(rel, `"make_latest":"legacy"`) || strings.Contains(rel, "previous_tag_name") {
		t.Errorf("root release changed: %q", rel)
	}
}

// -notes-file keeps the body the person wrote; no previous_tag_name next to it.
func TestNestedWithNotesFileSendsNoPreviousTag(t *testing.T) {
	p := withToken(t, "my notes")
	_, s, _, errs := nestedRun(t, "media/v0.2.0", map[string]string{"media/go.mod": "module github.com/o/r/media\n"},
		map[string][]string{"media/v": {"media/v0.1.0"}}, "-notes-file", p)
	rel := s.bodies["POST /repos/o/r/releases"]
	if !strings.Contains(rel, `"body":"my notes"`) || strings.Contains(rel, "previous_tag_name") {
		t.Errorf("release %q (%s)", rel, errs)
	}
}

// A tag that exists is not moved, nested or not, and before any merge.
func TestNestedExistingTagIsNotMoved(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/ref/tags/media/v0.2.0": reply(http.StatusOK, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "media/v0.2.0"}, &out, &errb); code != 1 || s.ran != 0 ||
		!strings.Contains(errb.String(), "not moved") {
		t.Errorf("exit %d, merged %d, stderr %q", code, s.ran, errb.String())
	}
}

// What the command line refuses before any network call.
func TestNestedTagShapeIsRefusedAtTheCommandLine(t *testing.T) {
	for _, tag := range []string{"media/1.0.0", "media/", "/v1.0.0", "a//b/v1.0.0", "../v1.0.0", "a/../v1.0.0", "./v1.0.0", "a b/v1.0.0", "media/x1.0.0"} {
		s := &stub{}
		install(t, s)
		var out, errb bytes.Buffer
		code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", tag}, &out, &errb)
		if code != 2 || len(s.calls) != 0 || !strings.Contains(errb.String(), "not a version tag") {
			t.Errorf("%q: exit %d, calls %v, stderr %q", tag, code, s.calls, errb.String())
		}
	}
	// A directory with a well-shaped tag but a malformed version is the order check's refusal.
	code, s, _, errs := nestedRun(t, "media/v1.2", nil, nil)
	if code == 0 || s.ran != 0 || !strings.Contains(errs, "vMAJOR.MINOR.PATCH") {
		t.Errorf("exit %d, merged %d, stderr %q", code, s.ran, errs)
	}
}

// The dry run of a merged pull request judges the go.mod too, and names the refusal.
func TestNestedDryRun(t *testing.T) {
	code, s, out, errs := nestedRun(t, "media/v0.2.0", map[string]string{"media/go.mod": "module github.com/o/r/media\n"}, nil, "-dry-run")
	if code != 0 || tagged(s) || !strings.Contains(out, "would tag media/v0.2.0 at cafebabe") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
	code, s, _, errs = nestedRun(t, "media/v0.2.0", map[string]string{"media/go.mod": "module github.com/o/r\n"}, nil, "-dry-run")
	if code != 1 || tagged(s) || !strings.Contains(errs, "would refuse media/v0.2.0") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	// An unreadable go.mod does not make a dry run unusable.
	s = &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3":               reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000"}`),
		"GET /repos/o/r/contents/media/go.mod": reply(http.StatusInternalServerError, `{}`),
	}}
	install(t, s)
	var o, e bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-dry-run", "o/r", "3", "media/v0.2.0"}, &o, &e); code != 0 ||
		!strings.Contains(e.String(), "could not read media's go.mod") {
		t.Errorf("exit %d, stderr %q", code, e.String())
	}
}

// -release-only on a nested tag: the release of an existing tag, not Latest.
func TestNestedReleaseOnly(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"media/v0.2.0"}, `"make_latest":"false"`},
		{[]string{"--make-latest", "media/v0.2.0"}, `"make_latest":"legacy"`},
		{[]string{"v0.2.0"}, `"make_latest":"legacy"`},
	} {
		s := &stub{byPath: map[string]*http.Response{
			"GET /repos/o/r/git/ref/tags/" + c.args[len(c.args)-1]: reply(http.StatusOK, `{}`),
			"POST /repos/o/r/releases":                             reply(http.StatusCreated, `{}`),
		}}
		install(t, s)
		var out, errb bytes.Buffer
		args := append([]string{"-token-file", withToken(t, "t"), "-release-only", "o/r"}, c.args...)
		if code := run(args, &out, &errb); code != 0 {
			t.Fatalf("%v: exit %d: %s", c.args, code, errb.String())
		}
		if rel := s.bodies["POST /repos/o/r/releases"]; !strings.Contains(rel, c.want) {
			t.Errorf("%v: release %q, want %s", c.args, rel, c.want)
		}
		if asked(s, "POST /repos/o/r/git/refs") {
			t.Error("-release-only created a tag")
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-release-only", "o/r", "media/1.0.0"}, &out, &errb); code != 2 {
		t.Errorf("a malformed nested tag: exit %d", code)
	}
}

func TestSplitTag(t *testing.T) {
	for _, c := range []struct {
		tag, dir, ver string
		ok            bool
	}{
		{"v1.2.3", "", "v1.2.3", true},
		{"media/v1.2.3", "media", "v1.2.3", true},
		{"cmd/wctr/v0.1.0", "cmd/wctr", "v0.1.0", true},
		{"1.2.3", "", "", false},
		{"media/1.2.3", "", "", false},
		{"media/", "", "", false},
		{"/v1.0.0", "", "", false},
		{"a//v1.0.0", "", "", false},
		{"a/../v1.0.0", "", "", false},
		{"a/./v1.0.0", "", "", false},
	} {
		dir, ver, ok := splitTag(c.tag)
		if dir != c.dir || ver != c.ver || ok != c.ok {
			t.Errorf("splitTag(%q) = %q %q %v, want %q %q %v", c.tag, dir, ver, ok, c.dir, c.ver, c.ok)
		}
	}
}

func TestModuleDirPath(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"module a.b/c\n", "a.b/c", true},
		{"go 1.27\n\nmodule   a.b/c  // trailing\nrequire x v1\n", "a.b/c", true},
		{"module \"a.b/c\"\n", "a.b/c", true},
		{"module `a.b/c`\n", "a.b/c", true},
		{"module (\n\t// c\n\ta.b/c\n)\n", "a.b/c", true},
		{"module(\n a.b/c\n)\n", "a.b/c", true},
		{"modules a.b/c\n", "", false},
		{"module\n", "", false},
		{"", "", false},
		{"// module a.b/c\n", "", false},
	} {
		got, ok := moduleDirPath(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("moduleDirPath(%q) = %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPathMajor(t *testing.T) {
	for _, c := range []struct {
		in, base string
		major    uint64
	}{
		{"a/b", "a/b", 0}, {"a/b/v2", "a/b", 2}, {"a/b/v10", "a/b", 10}, {"a/b/v1", "a/b/v1", 0},
		{"a/b/v0", "a/b/v0", 0}, {"a/b/v02", "a/b/v02", 0}, {"a/b/vx", "a/b/vx", 0}, {"v2", "v2", 0},
	} {
		if base, major := pathMajor(c.in); base != c.base || major != c.major {
			t.Errorf("pathMajor(%q) = %q %d, want %q %d", c.in, base, major, c.base, c.major)
		}
	}
}

// Backfill must not confuse the lines: nested tags are released too, and the
// notes of each compare with ITS previous tag.
func TestBackfillKeepsRootAndNestedTagsApart(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC) }
	st := repoState{
		released: map[string]bool{},
		tags: []tagInfo{
			{"media/v0.2.0", d(5)}, {"v1.1.0", d(4)}, {"media/v0.1.0", d(3)}, {"v1.0.0", d(2)}, {"audio/v1.0.0", d(1)},
		},
	}
	for tag, want := range map[string]string{
		"v1.1.0":       "v1.0.0",
		"media/v0.2.0": "media/v0.1.0",
		"v1.0.0":       "",
		"media/v0.1.0": "",
		"audio/v1.0.0": "",
	} {
		var ti tagInfo
		for _, x := range st.tags {
			if x.Name == tag {
				ti = x
			}
		}
		if got := st.previous(ti); got != want {
			t.Errorf("previous(%s) = %q, want %q", tag, got, want)
		}
	}
	st.tags = append(st.tags, tagInfo{"latest", d(6)}, tagInfo{"media/notes", d(6)})
	got := map[string]bool{}
	for _, m := range st.missing("", time.Time{}) {
		got[m.Name] = true
	}
	for _, want := range []string{"media/v0.2.0", "media/v0.1.0", "audio/v1.0.0", "v1.1.0", "v1.0.0"} {
		if !got[want] {
			t.Errorf("missing() lacks %s: %v", want, got)
		}
	}
	if got["latest"] || got["media/notes"] || len(got) != 5 {
		t.Errorf("missing() took a tag that is not a version: %v", got)
	}
}
