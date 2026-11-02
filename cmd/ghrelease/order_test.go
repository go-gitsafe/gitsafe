package main

import (
	"bytes"
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
