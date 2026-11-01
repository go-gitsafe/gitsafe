package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reply builds an HTTP response the seam can return.
func reply(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Status:     http.StatusText(code),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// stub installs a fake GitHub and a fake merge, and reports what was asked.
type stub struct {
	calls  []string
	bodies map[string]string // request body, by "METHOD path"
	byPath map[string]*http.Response
	doErr  error
	merged error
	ran    int
}

func install(t *testing.T, s *stub) {
	t.Helper()
	oh, om, ou := httpDo, runMerge, userHomeDir
	t.Cleanup(func() { httpDo, runMerge, userHomeDir = oh, om, ou })
	httpDo = func(req *http.Request) (*http.Response, error) {
		s.calls = append(s.calls, req.Method+" "+req.URL.Path)
		if req.Body != nil {
			b, _ := io.ReadAll(req.Body)
			if s.bodies == nil {
				s.bodies = map[string]string{}
			}
			s.bodies[req.Method+" "+req.URL.Path] = string(b)
		}
		if s.doErr != nil {
			return nil, s.doErr
		}
		if r, ok := s.byPath[req.Method+" "+req.URL.Path]; ok {
			return r, nil
		}
		// Main CI is green unless a test says otherwise: these tests are about
		// the merge and the tag, and maincheck_test.go is about the wait.
		if req.Method == http.MethodGet && req.URL.Path == "/repos/o/r/actions/runs" {
			return reply(http.StatusOK, `{"total_count":1,"workflow_runs":[{"id":1,"name":"ci","status":"completed","conclusion":"success","head_sha":"`+
				req.URL.Query().Get("head_sha")+`","html_url":"https://example.test/run/1"}]}`), nil
		}
		return reply(http.StatusNotFound, "{}"), nil
	}
	runMerge = func(string, int, io.Writer, io.Writer) error { s.ran++; return s.merged }
}

// withToken points the token file at a temporary one.
func withToken(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTargetReadsWhatItWasGiven(t *testing.T) {
	repo, n, tag, err := target([]string{"go-authn/fido", "12", "v0.2.0"})
	if err != nil || repo != "go-authn/fido" || n != 12 || tag != "v0.2.0" {
		t.Errorf("target = %q %d %q, %v", repo, n, tag, err)
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no arguments", nil, "pull request number and a tag"},
		{"one argument", []string{"7"}, "pull request number and a tag"},
		{"four arguments", []string{"a", "b", "c", "d"}, "pull request number and a tag"},
		{"a number that is not one", []string{"o/r", "twelve", "v1.0.0"}, "not a pull request number"},
		{"a tag with no v", []string{"o/r", "12", "1.0.0"}, "leading v"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, _, _, err := target(c.args); err == nil {
				t.Fatal("accepted")
			} else if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestTokenRefusesWhatCannotBeAToken(t *testing.T) {
	if _, err := token(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing token file was accepted")
	}
	if _, err := token(withToken(t, "   \n")); err == nil {
		t.Error("an empty token file was accepted")
	}
	got, err := token(withToken(t, "  secret\n"))
	if err != nil || got != "secret" {
		t.Errorf("token = %q, %v", got, err)
	}
	// And with no path it asks for the home directory, which can fail.
	old := userHomeDir
	t.Cleanup(func() { userHomeDir = old })
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	if _, err := token(""); err == nil {
		t.Error("a machine with no home directory was accepted")
	}
}

// TestATagThatExistsIsNotMovedAndNothingIsMerged is the guard that matters
// most: finding out after the merge would leave a merged pull request and no
// release, which only somebody who knew what half-happened could finish.
func TestATagThatExistsIsNotMovedAndNothingIsMerged(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/ref/tags/v1.0.0": reply(http.StatusOK, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if s.ran != 0 {
		t.Error("it merged before checking the tag")
	}
	if !strings.Contains(errb.String(), "not moved") {
		t.Errorf("stderr = %q", errb.String())
	}
}

// TestARefusedMergeIsNotTagged. This is the whole reason the command exists:
// merging and tagging as two commands let a refusal through a pipe.
func TestARefusedMergeIsNotTagged(t *testing.T) {
	s := &stub{merged: errors.New("exit status 1")}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "not tagging") {
		t.Errorf("stderr = %q", errb.String())
	}
	for _, c := range s.calls {
		if strings.HasPrefix(c, "POST") {
			t.Errorf("it tagged anyway: %s", c)
		}
	}
}

func TestAMergeThatWorksIsTaggedAtItsMergeCommit(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3":   reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000"}`),
		"POST /repos/o/r/git/refs": reply(http.StatusCreated, `{}`),
		"POST /repos/o/r/releases": reply(http.StatusCreated, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "cafebabe") {
		t.Errorf("stdout = %q, want the merge commit named", out.String())
	}
	if s.ran != 1 {
		t.Errorf("merged %d time(s)", s.ran)
	}
	// go-gitsafe/gitsafe#59: the GitHub release is created too, for this
	// tag, with GitHub's generated notes.
	rel := s.bodies["POST /repos/o/r/releases"]
	if !strings.Contains(rel, `"tag_name":"v1.0.0"`) || !strings.Contains(rel, `"generate_release_notes":true`) ||
		!strings.Contains(rel, `"make_latest":"legacy"`) || !strings.Contains(out.String(), "released v1.0.0") {
		t.Errorf("release request %q, stdout %q", rel, out.String())
	}
}

// The notes of -notes-file are the release's body, not GitHub's.
func TestNotesFile(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3":   reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000"}`),
		"POST /repos/o/r/git/refs": reply(http.StatusCreated, `{}`),
		"POST /repos/o/r/releases": reply(http.StatusCreated, `{}`),
	}}
	install(t, s)
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("Hand-written notes."), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-notes-file", notes, "o/r", "3", "v1.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	rel := s.bodies["POST /repos/o/r/releases"]
	if !strings.Contains(rel, `"body":"Hand-written notes."`) || strings.Contains(rel, "generate_release_notes") {
		t.Errorf("release request %q", rel)
	}
}

// The release fails after the tag was pushed: the command fails loudly, says
// what exists and what does not, and names the resume, which then creates
// the release for the EXISTING tag without asking to tag anything.
func TestReleaseMissingAfterTheTagAndItsResume(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3":   reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"cafebabe0000"}`),
		"POST /repos/o/r/git/refs": reply(http.StatusCreated, `{}`),
		"POST /repos/o/r/releases": reply(http.StatusUnprocessableEntity, `{"message":"Validation Failed"}`),
	}}
	install(t, s)
	tok := withToken(t, "t")
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", tok, "o/r", "3", "v1.0.0"}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "tag created, release missing") || !strings.Contains(errb.String(), "ghrelease -release-only o/r v1.0.0") {
		t.Errorf("stderr = %q", errb.String())
	}

	// The resume: the tag exists, the release does not.
	s2 := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/ref/tags/v1.0.0": reply(http.StatusOK, `{}`),
		"POST /repos/o/r/releases":           reply(http.StatusCreated, `{}`),
	}}
	install(t, s2)
	out.Reset()
	errb.Reset()
	if code := run([]string{"-token-file", tok, "-release-only", "o/r", "v1.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("resume: exit %d: %s", code, errb.String())
	}
	for _, c := range s2.calls {
		if c == "POST /repos/o/r/git/refs" || strings.HasPrefix(c, "PATCH") || strings.HasPrefix(c, "DELETE") {
			t.Errorf("the resume touched the tag: %s", c)
		}
	}
	if s2.ran != 0 {
		t.Error("the resume merged")
	}
	if !strings.Contains(out.String(), "not moved") {
		t.Errorf("stdout = %q", out.String())
	}

	// A tag that does not exist is not created by the resume.
	install(t, &stub{})
	errb.Reset()
	if code := run([]string{"-token-file", tok, "-release-only", "o/r", "v9.9.9"}, &out, &errb); code != 1 ||
		!strings.Contains(errb.String(), "does not create one") {
		t.Errorf("resume without a tag: exit %d, %q", code, errb.String())
	}

	// A release that exists already: nothing to do.
	install(t, &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/ref/tags/v1.0.0":  reply(http.StatusOK, `{}`),
		"GET /repos/o/r/releases/tags/v1.0.0": reply(http.StatusOK, `{}`),
	}})
	out.Reset()
	if code := run([]string{"-token-file", tok, "-release-only", "o/r", "v1.0.0"}, &out, &errb); code != 0 ||
		!strings.Contains(out.String(), "already has a release") {
		t.Errorf("resume with a release: exit %d, %q", code, out.String())
	}
}

func TestAMergeWhoseCommitCannotBeFound(t *testing.T) {
	for _, c := range []struct {
		name, body string
		code       int
		want       string
	}{
		{"GitHub says it is not merged", `{"merged":false}`, http.StatusOK, "not merged"},
		{"GitHub gives no commit", `{"merged":true}`, http.StatusOK, "no merge commit"},
		{"GitHub answers badly", `{}`, http.StatusInternalServerError, "for the pull request"},
		{"the answer is not JSON", `not json`, http.StatusOK, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &stub{byPath: map[string]*http.Response{
				"GET /repos/o/r/pulls/3": reply(c.code, c.body),
			}}
			install(t, s)
			var out, errb bytes.Buffer
			if got := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); got != 1 {
				t.Fatalf("exit %d, want 1", got)
			}
			if !strings.Contains(errb.String(), "merged, but") {
				t.Errorf("stderr %q does not say the merge already happened", errb.String())
			}
			if c.want != "" && !strings.Contains(errb.String(), c.want) {
				t.Errorf("stderr %q does not mention %q", errb.String(), c.want)
			}
		})
	}
}

func TestATagGitHubRefuses(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3":   reply(http.StatusOK, `{"merged":true,"merge_commit_sha":"abc"}`),
		"POST /repos/o/r/git/refs": reply(http.StatusUnprocessableEntity, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "refused the tag") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestGitHubThatCannotBeReached(t *testing.T) {
	s := &stub{doErr: errors.New("no network")}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if strings.Contains(errb.String(), "secret") {
		t.Error("the error quoted the token")
	}
}

func TestAStrangeAnswerLookingForTheTag(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/ref/tags/v1.0.0": reply(http.StatusForbidden, `{}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "looking for the tag") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestDryRunMergesNothing(t *testing.T) {
	s := &stub{}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-dry-run", "o/r", "3", "v1.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if s.ran != 0 {
		t.Error("a dry run merged")
	}
	if !strings.Contains(out.String(), "would merge") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestBadArgumentsAndTokens(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-nope"}, &out, &errb); code != 2 {
		t.Errorf("an unknown flag = %d, want 2", code)
	}
	errb.Reset()
	if code := run([]string{"o/r", "3"}, &out, &errb); code != 2 {
		t.Errorf("a missing tag = %d, want 2", code)
	}
	errb.Reset()
	if code := run([]string{"-token-file", filepath.Join(t.TempDir(), "absent"), "o/r", "3", "v1.0.0"}, &out, &errb); code != 1 {
		t.Errorf("a missing token = %d, want 1", code)
	}
}

func TestShortNamesACommitBriefly(t *testing.T) {
	if got := short("cafebabedeadbeef"); got != "cafebabe" {
		t.Errorf("short = %q", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short of a short hash = %q", got)
	}
}

// A 403 from GitHub's burst limit says so, and does not send anybody to
// reissue a token that works -- what the bare status did on 2026-10-04.
func TestABurstLimitIsNotADeadCredential(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/git/ref/tags/v1.0.0": reply(http.StatusForbidden,
			`{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "o/r", "3", "v1.0.0"}, &out, &errb); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	e := errb.String()
	if !strings.Contains(e, "burst limit") || !strings.Contains(e, "credential is fine") || strings.Contains(e, "expired") {
		t.Errorf("stderr = %q", e)
	}
}

// ⛔ These three tests pin down the reason for the change of 2026-10-11.
//
// The dry run said "would merge o/r#3, then tag its merge commit v1.0.0"
// whatever state the pull request was in. For an ALREADY merged request that
// reads as "will attempt to merge" — whereas ghmerge returns 0 on an already
// merged request, so ghrelease would have gone on to tag the right commit. That
// wording sent somebody around the command on 2026-10-10, to place the tag with
// `gh api .../git/refs` and a sha read from `commits/main`; main had moved in the
// meantime under a peer's merge. It is the fourth occurrence of the mistake
// described at the head of this file, in a new spelling.
func TestADryRunOnAnAlreadyMergedRequestNamesTheCommitItWouldTag(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3": reply(http.StatusOK,
			`{"merged":true,"merge_commit_sha":"8f9af5be0123456789abcdef"}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-dry-run", "o/r", "3", "v1.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if s.ran != 0 {
		t.Error("a dry run merged")
	}
	for _, want := range []string{"already merged", "8f9af5be", "v1.0.0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout does not contain %q: %q", want, out.String())
		}
	}
	// ⛔ And it must NOT read as an intention to merge, which is the whole point.
	if strings.Contains(out.String(), "would merge") {
		t.Errorf("an already merged request still reads as a merge: %q", out.String())
	}
}

func TestADryRunOnAnOpenRequestSaysTheMergeCommitDoesNotExistYet(t *testing.T) {
	s := &stub{byPath: map[string]*http.Response{
		"GET /repos/o/r/pulls/3": reply(http.StatusOK, `{"merged":false,"merge_commit_sha":""}`),
	}}
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-dry-run", "o/r", "3", "v1.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "would merge") ||
		!strings.Contains(out.String(), "does not exist yet") {
		t.Errorf("stdout = %q", out.String())
	}
}

// ⭐ And the case that must stay USABLE: a dry run that finds the tag free but
// cannot read the pull request says what it would attempt, says on stderr that
// it could not name the commit, and exits 0. Refusing here would make -dry-run
// useless exactly when somebody is being careful.
//
// ⛔ A first version of this test used doErr to cut the whole network. That was a
// false premise: tagExists runs BEFORE this block, so a dry run already did not
// survive a general outage. The branch is only reached when the tag lookup
// succeeds — a 404 means "free" — and reading the pull request fails.
func TestADryRunThatCannotReadTheRequestSaysSoAndStillWorks(t *testing.T) {
	s := &stub{} // 404 everywhere: the tag is free, the request unreadable
	install(t, s)
	var out, errb bytes.Buffer
	if code := run([]string{"-token-file", withToken(t, "t"), "-dry-run", "o/r", "3", "v1.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "would merge") {
		t.Errorf("stdout = %q", out.String())
	}
	if !strings.Contains(errb.String(), "did NOT name the commit") {
		t.Errorf("stderr does not say the commit was not named: %q", errb.String())
	}
}
