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
		if s.doErr != nil {
			return nil, s.doErr
		}
		if r, ok := s.byPath[req.Method+" "+req.URL.Path]; ok {
			return r, nil
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
