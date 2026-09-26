package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGitHub answers the handful of endpoints ghnew uses, and records the bodies
// it was sent so a test can assert what was ASKED rather than only what came back.
type fakeGitHub struct {
	t             *testing.T
	exists        bool   // GET /repos/o/n before creating
	createStatus  int    // POST .../repos
	orgStatus     int    // when set, the org endpoint answers this instead
	branchName    string // what GitHub called the initial branch
	branchAfter   int    // how many GETs before the ref appears; -1 means never
	createdBodies []map[string]any
	patched       []map[string]any
	refGets       int
}

func (f *fakeGitHub) server() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/repos") && r.Method == http.MethodPost:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				f.t.Fatalf("create body: %v", err)
			}
			f.createdBodies = append(f.createdBodies, body)
			status := f.createStatus
			if strings.HasPrefix(path, "/orgs/") && f.orgStatus != 0 {
				status = f.orgStatus
			}
			w.WriteHeader(status)
			io.WriteString(w, `{"message":"made up"}`)

		case strings.Contains(path, "/git/ref/heads/"):
			f.refGets++
			if f.branchAfter < 0 || f.refGets <= f.branchAfter {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"message":"Not Found"}`)
				return
			}
			io.WriteString(w, `{"object":{"sha":"0123456789abcdef"}}`)

		case r.Method == http.MethodPatch:
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.patched = append(f.patched, body)
			io.WriteString(w, `{}`)

		case r.Method == http.MethodGet && strings.HasPrefix(path, "/repos/"):
			// The existence probe and the default-branch read share this path.
			if !f.exists && len(f.createdBodies) == 0 {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"message":"Not Found"}`)
				return
			}
			name := f.branchName
			if name == "" {
				name = "main"
			}
			io.WriteString(w, `{"default_branch":"`+name+`"}`)

		default:
			f.t.Fatalf("unexpected %s %s", r.Method, path)
		}
	})
	return httptest.NewServer(mux)
}

// withFake points ghnew at the fake and gives it a token file.
//
// ⛔ The file holds a string that does not LOOK like a credential. A realistic
// placeholder in a test is a small hazard of its own: it turns up in greps for
// leaked tokens, and the guard on this machine refuses a command line carrying
// one -- correctly, since a guard that reasons about shape cannot know a fake
// from the real thing.
func withFake(t *testing.T, f *fakeGitHub) (tokenPath string) {
	t.Helper()
	srv := f.server()
	t.Cleanup(srv.Close)
	oldBase := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = oldBase })

	oldSleep, oldWait := sleep, branchWait
	sleep = func(time.Duration) {} // the retry loop must not cost a second per test
	branchWait = 0
	t.Cleanup(func() { sleep, branchWait = oldSleep, oldWait })

	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("stand-in-for-a-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestItAsksForAnInitialCommit(t *testing.T) {
	f := &fakeGitHub{t: t, createStatus: http.StatusCreated, branchAfter: 0}
	token := withFake(t, f)

	var out bytes.Buffer
	if err := run([]string{"-public", "-token-file", token, "org/thing", "a thing"}, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if len(f.createdBodies) != 1 {
		t.Fatalf("%d create calls, want 1", len(f.createdBodies))
	}
	body := f.createdBodies[0]
	// ⛔ THE assertion. Everything else about this program is convenience.
	if body["auto_init"] != true {
		t.Errorf("auto_init = %v, want true: without it the repository has no "+
			"branch and the first commit cannot go through a pull request", body["auto_init"])
	}
	if body["private"] != false {
		t.Errorf("private = %v, want false for -public", body["private"])
	}
	if body["license_template"] != "bsd-3-clause" {
		t.Errorf("license_template = %v, want the fleet's default", body["license_template"])
	}
	if !strings.Contains(out.String(), "so a pull request has a base") {
		t.Errorf("output does not say the branch is there:\n%s", out.String())
	}
}

// TestItRefusesWhenTheBranchNeverAppears is the case this program exists for. A
// repository created with no default branch is the exact situation that leads to
// pushing to main, so being told "created" and nothing else is the wrong outcome.
func TestItRefusesWhenTheBranchNeverAppears(t *testing.T) {
	f := &fakeGitHub{t: t, createStatus: http.StatusCreated, branchAfter: -1}
	token := withFake(t, f)

	var out bytes.Buffer
	err := run([]string{"-public", "-token-file", token, "org/thing"}, &out)
	if err == nil {
		t.Fatal("a repository with no default branch was reported as a success")
	}
	for _, want := range []string{"do NOT push", "has no base"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q: the person reading this is "+
				"about to decide what to do next", err, want)
		}
	}
	if f.refGets < 2 {
		t.Errorf("looked for the branch %d times, want a retry: GitHub writes the "+
			"initial commit a moment after answering 201", f.refGets)
	}
}

func TestItRetriesUntilTheBranchAppears(t *testing.T) {
	f := &fakeGitHub{t: t, createStatus: http.StatusCreated, branchAfter: 3}
	token := withFake(t, f)
	var out bytes.Buffer
	if err := run([]string{"-public", "-token-file", token, "org/thing"}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.refGets != 4 {
		t.Errorf("%d attempts, want 4 (three misses then the ref)", f.refGets)
	}
}

func TestItFallsBackToTheUserEndpoint(t *testing.T) {
	f := &fakeGitHub{
		t: t, createStatus: http.StatusCreated,
		orgStatus: http.StatusNotFound, branchAfter: 0,
	}
	token := withFake(t, f)
	var out bytes.Buffer
	if err := run([]string{"-private", "-token-file", token, "someone/thing"}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(f.createdBodies) != 2 {
		t.Fatalf("%d create calls, want 2: the org endpoint then /user/repos", len(f.createdBodies))
	}
	if f.createdBodies[1]["auto_init"] != true {
		t.Error("the fallback call dropped auto_init, so the repository it makes has no branch")
	}
	if f.createdBodies[1]["private"] != true {
		t.Error("-private did not reach the fallback call")
	}
}

func TestItRenamesTheInitialBranchWhenGitHubPicksAnother(t *testing.T) {
	f := &fakeGitHub{
		t: t, createStatus: http.StatusCreated, branchAfter: 0, branchName: "master",
	}
	token := withFake(t, f)
	var out bytes.Buffer
	if err := run([]string{"-public", "-token-file", token, "org/thing"}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(f.patched) != 1 || f.patched[0]["default_branch"] != "main" {
		t.Errorf("patched %v, want one call setting default_branch to main: the "+
			"initial branch is named from the ACCOUNT setting, not from the request",
			f.patched)
	}
}

func TestItRefusesARepositoryThatAlreadyExists(t *testing.T) {
	f := &fakeGitHub{t: t, exists: true, createStatus: http.StatusCreated, branchAfter: 0}
	token := withFake(t, f)
	var out bytes.Buffer
	err := run([]string{"-public", "-token-file", token, "org/thing"}, &out)
	if err == nil {
		t.Fatal("an existing repository was created over")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want it to say the repository is already there", err)
	}
	if len(f.createdBodies) != 0 {
		t.Errorf("%d create calls after finding it exists, want 0", len(f.createdBodies))
	}
}

func TestItInsistsOnAVisibility(t *testing.T) {
	f := &fakeGitHub{t: t, createStatus: http.StatusCreated, branchAfter: 0}
	token := withFake(t, f)
	for _, args := range [][]string{
		{"-token-file", token, "org/thing"},
		{"-public", "-private", "-token-file", token, "org/thing"},
	} {
		var out bytes.Buffer
		if err := run(args, &out); err == nil {
			t.Errorf("%v was accepted: neither default is safe to assume", args)
		}
	}
	if len(f.createdBodies) != 0 {
		t.Errorf("%d repositories created while the visibility was unclear", len(f.createdBodies))
	}
}

func TestThereIsNoWayToSkipTheInitialCommit(t *testing.T) {
	// ⛔ A guard on the program's own shape. An option to switch auto_init off
	// would be an option to have the defect back, so its absence is asserted
	// rather than left to whoever reads the flags next.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, `"auto_init": true`) {
		t.Fatal("auto_init is not set unconditionally, and this whole program is that line")
	}
	for _, forbidden := range []string{"no-init", "noInit", "skipInit"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("main.go mentions %q: there must be no way to create a "+
				"repository without a default branch", forbidden)
		}
	}
}

func TestSplitRepoRefusesAGuess(t *testing.T) {
	for _, bad := range []string{"thing", "", "/thing", "org/", "a/b/c", "org//"} {
		if _, _, err := splitRepo(bad); err == nil {
			t.Errorf("splitRepo(%q) was accepted: creating a repository under the "+
				"wrong account is not something a person notices straight away", bad)
		}
	}
	owner, name, err := splitRepo("go-compressions/adc")
	if err != nil || owner != "go-compressions" || name != "adc" {
		t.Errorf("splitRepo gave %q/%q, %v", owner, name, err)
	}
}
