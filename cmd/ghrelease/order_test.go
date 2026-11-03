package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// existing builds a matching-refs answer for the given tags.
func existing(tags ...string) string {
	var parts []string
	for _, t := range tags {
		parts = append(parts, `{"ref":"refs/tags/`+t+`"}`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// orderRun runs ghrelease for o/r#3 as tag, with the repository holding tags.
func orderRun(t *testing.T, tag string, tags []string, args ...string) (int, *stub, string, string) {
	t.Helper()
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/matching-refs/tags/v": reply(http.StatusOK, existing(tags...)),
		"GET /repos/o/r/pulls/3":                  reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000","base":{"ref":"main"}}`),
		"POST /repos/o/r/git/refs":                reply(http.StatusCreated, `{}`),
		"POST /repos/o/r/releases":                reply(http.StatusCreated, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	full := append([]string{"-token-file", withToken(t, "t"), "-no-backfill"}, args...)
	code := run(append(full, "o/r", "3", tag), &out, &errb)
	return code, s, out.String(), errb.String()
}

func tagged(s *stub) bool { _, ok := s.bodies["POST /repos/o/r/git/refs"]; return ok }

// The incident of 2026-11-02: v0.7.1 over an existing v0.9.0.
func TestALowerTagIsRefusedWithSuggestions(t *testing.T) {
	code, s, _, errs := orderRun(t, "v0.7.1", []string{"v0.8.0", "v0.9.0", "v0.7.0"})
	if code == 0 || tagged(s) || s.ran != 0 {
		t.Fatalf("exit %d, tagged %v, merged %d", code, tagged(s), s.ran)
	}
	for _, want := range []string{"lower than", "v0.9.0", "PATCH v0.9.1", "MINOR v0.10.0", "MAJOR v1.0.0", "-allow-lower"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q: %q", want, errs)
		}
	}
}

func TestAnEqualTagIsRefusedByTheOrderCheckToo(t *testing.T) {
	// The tag-exists check says it first in real life; the order check must
	// hold on its own (the ref listing can be ahead of the single-ref lookup).
	code, s, _, errs := orderRun(t, "v0.9.0", []string{"v0.9.0"})
	if code == 0 || tagged(s) || !strings.Contains(errs, "equal to") {
		t.Fatalf("exit %d, tagged %v, stderr %q", code, tagged(s), errs)
	}
}

func TestAGreaterTagIsTagged(t *testing.T) {
	code, s, _, errs := orderRun(t, "v0.10.0", []string{"v0.9.0", "v0.2.0", "v0.9.9"})
	if code != 0 || !tagged(s) {
		t.Fatalf("exit %d, tagged %v: %s", code, tagged(s), errs)
	}
	// Numeric, not lexical: v0.10.0 > v0.9.0 (a string comparison says the reverse).
	if !strings.Contains(s.bodies["POST /repos/o/r/releases"], `"make_latest":"legacy"`) {
		t.Errorf("release %q", s.bodies["POST /repos/o/r/releases"])
	}
}

func TestAllowLowerTagsAndIsNotLatest(t *testing.T) {
	code, s, _, errs := orderRun(t, "v0.7.1", []string{"v0.9.0"}, "-allow-lower")
	if code != 0 || !tagged(s) {
		t.Fatalf("exit %d, tagged %v: %s", code, tagged(s), errs)
	}
	rel := s.bodies["POST /repos/o/r/releases"]
	if !strings.Contains(rel, `"make_latest":"false"`) || strings.Contains(rel, "legacy") {
		t.Errorf("a backport was allowed to become Latest: %q", rel)
	}
}

// Pre-releases never make a stable tag unreachable (semver.org item 9, GitHub's
// Latest skips them): v1.0.0-rc.1 does not block v0.9.1, v0.9.0-rc.1 does not
// block v0.9.0, and v0.9.0 over v0.9.0-rc.1 is above it.
func TestPreReleaseOrdering(t *testing.T) {
	for _, c := range []struct {
		tag  string
		have []string
		ok   bool
	}{
		{"v0.9.1", []string{"v0.9.0", "v1.0.0-rc.1"}, true},
		{"v0.9.0", []string{"v0.9.0-rc.1", "v0.9.0-rc.2"}, true},
		{"v0.9.0", []string{"v0.9.0-rc.1", "v0.9.1"}, false},
		{"v1.0.0", []string{"v1.0.0-rc.1"}, true},
		{"v0.9.1", []string{"v0.9.0+build.5", "latest", "v1", "vfoo"}, true}, // build metadata ignored; non-semver ignored
		{"v0.9.0", []string{"v0.9.0+build.5"}, false},
	} {
		code, s, _, errs := orderRun(t, c.tag, c.have)
		if (code == 0) != c.ok || tagged(s) != c.ok {
			t.Errorf("%s over %v: exit %d, tagged %v, want ok=%v: %s", c.tag, c.have, code, tagged(s), c.ok, errs)
		}
	}
}

func TestAMalformedTagIsRefused(t *testing.T) {
	for _, tag := range []string{"v1", "v1.2", "v1.2.3.4", "v01.2.3", "v1.2.3-rc.1", "v1.2.3+b", "v1.x.3", "vv1.2.3", "v-1.2.3"} {
		code, s, _, errs := orderRun(t, tag, nil)
		if code == 0 || tagged(s) || s.ran != 0 || !strings.Contains(errs, "vMAJOR.MINOR.PATCH") {
			t.Errorf("%s: exit %d, tagged %v, merged %d, stderr %q", tag, code, tagged(s), s.ran, errs)
		}
	}
}

// ⛔ Planted control: with the check put back to the old behaviour (accept
// everything) the SAME lower-tag scenario must tag. Otherwise the refusal test
// could pass because nothing was ever tagged in this fixture.
func TestControlTheOldBehaviourTagsALowerTag(t *testing.T) {
	old := orderCheck
	t.Cleanup(func() { orderCheck = old })
	orderCheck = func(string, string, string, bool, *budget) (bool, string, error) { return false, "", nil }
	code, s, _, errs := orderRun(t, "v0.7.1", []string{"v0.9.0"})
	if code != 0 || !tagged(s) {
		t.Fatalf("the control did not reproduce the defect: exit %d, tagged %v: %s", code, tagged(s), errs)
	}
}

// replay is reply for a response the stub may be asked for more than once: the
// body rewinds on Close, so a second request reads the same bytes instead of an
// empty stream.
//
// It exists for one test, and the distinction is the whole point of that test.
// Real GitHub answers the same question twice with the same answer; a fixture
// whose body is exhausted after the first read turns "this code asked twice"
// into a decode error on the second, which is a DIFFERENT failure from the one
// production had. A regression test for asking twice needs the server's
// behaviour, not the fixture's.
func replay(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Status:     http.StatusText(code),
		Body:       rewindOnClose{strings.NewReader(body)},
	}
}

type rewindOnClose struct{ r *strings.Reader }

func (b rewindOnClose) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b rewindOnClose) Close() error               { _, err := b.r.Seek(0, io.SeekStart); return err }

// TestAHundredTagsAreListedInOneRequest is the regression for gitsafe#82.
//
// matching-refs is NOT paginated: it returns every matching ref in one response
// and ignores page and per_page. Measured 2026-11-03 on go-ansible/modules,
// which has 109 v* tags — `?per_page=100&page=1` and `&page=2` both return the
// same 109.
//
// The first version of listTags sent page/per_page anyway and ended its walk on
// the first SHORT page. A repository with 100 or more v* tags has no short
// page, so the walk ran its 30-page bound out and refused with "more than 3000
// tags" — a number computed from the bound, not counted from anything. It
// blocked go-ansible/modules (109 tags), go-ansible/cli (176) and
// go-ansible/playbook (177) within a day of shipping.
//
// Every test above uses a fixture of three or four tags, which fits in one
// page, which is exactly why none of them saw it. This one uses 120.
func TestAHundredTagsAreListedInOneRequest(t *testing.T) {
	tags := make([]string, 0, 120)
	for i := range 120 {
		tags = append(tags, fmt.Sprintf("v0.%d.0", i))
	}
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/matching-refs/tags/v": replay(http.StatusOK, existing(tags...)),
		"GET /repos/o/r/pulls/3":                  reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000","base":{"ref":"main"}}`),
		"POST /repos/o/r/git/refs":                reply(http.StatusCreated, `{}`),
		"POST /repos/o/r/releases":                reply(http.StatusCreated, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	code := run([]string{"-token-file", withToken(t, "t"), "-no-backfill", "o/r", "3", "v0.120.0"}, &out, &errb)
	if code != 0 || !tagged(s) {
		t.Fatalf("exit %d, tagged %v: %s", code, tagged(s), errb.String())
	}

	// The other half, and the one that pins the FIX rather than its effect:
	// the list is fetched once. A loop that asked again would still reach the
	// right answer here, and this test would pass on a bug that costs thirty
	// API calls per release against a rate limit the tool budgets for.
	asked := 0
	for _, c := range s.calls {
		if c == "GET /repos/o/r/git/matching-refs/tags/v" {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("the tag list was requested %d times, want 1: matching-refs is not paginated", asked)
	}

	// The control: the fixture really did carry more than one page's worth,
	// and the highest of them really is below the tag asked for. Without it a
	// listing that returned nothing would pass every assertion above.
	if len(tags) <= 100 {
		t.Fatalf("control failed: the fixture has %d tags, which fits in one page", len(tags))
	}
	if !strings.Contains(s.bodies["POST /repos/o/r/git/refs"], "v0.120.0") {
		t.Errorf("the ref created was not the tag asked for: %q", s.bodies["POST /repos/o/r/git/refs"])
	}
}

func TestAnUnreadableTagListIsNotTagged(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/matching-refs/tags/v": reply(http.StatusInternalServerError, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); code == 0 || s.ran != 0 || tagged(s) {
		t.Errorf("exit %d, merged %d, stderr %q", code, s.ran, errb.String())
	}
}
