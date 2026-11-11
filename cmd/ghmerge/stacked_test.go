package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

// isStackedList says whether r asks which open pull requests are based on a
// branch (GET /repos/o/r/pulls?base=...). The fakes that do not care answer [].
func isStackedList(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls")
}

// stackFake is a GitHub with pull requests stacked on a-branch.
type stackFake struct {
	stacked []int          // numbers of open PRs whose base is a-branch, in order
	base    map[int]string // current base of each stacked PR
	refuse  map[int]int    // PATCH status to answer for a number (default 200)
	noop    map[int]bool   // answer 200 to the PATCH without changing the base
	events  []string       // "retarget N", "delete", "list page P"
	listErr bool
}

func newStackFake(t *testing.T, stacked ...int) *stackFake {
	t.Helper()
	f := &stackFake{base: map[int]string{}, refuse: map[int]int{}, noop: map[int]bool{}, stacked: stacked}
	for _, n := range stacked {
		f.base[n] = "a-branch"
	}
	runs := []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case isStackedList(r):
			if f.listErr {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			q := r.URL.Query()
			if q.Get("state") != "open" || q.Get("base") != "a-branch" || q.Get("per_page") != "100" {
				t.Errorf("wrong list query: %s", r.URL.RawQuery)
			}
			page, _ := strconv.Atoi(q.Get("page"))
			f.events = append(f.events, "list page "+q.Get("page"))
			lo, hi := (page-1)*100, page*100
			out := []map[string]any{}
			for i := lo; i < hi && i < len(f.stacked); i++ {
				out = append(out, map[string]any{"number": f.stacked[i]})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasSuffix(p, "/merge"):
			fmt.Fprint(w, `{"merged":true}`)
		case strings.Contains(p, "/check-runs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": runs})
		case strings.HasSuffix(p, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"statuses": []any{}})
		case strings.Contains(p, "/git/refs/heads/"):
			f.events = append(f.events, "delete")
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(p, "/pulls/"):
			n, _ := strconv.Atoi(p[strings.LastIndex(p, "/")+1:])
			if n == 1 {
				pr := green()
				pr["base"] = map[string]any{"ref": "main"}
				_ = json.NewEncoder(w).Encode(pr)
				return
			}
			if r.Method == http.MethodPatch {
				b, _ := io.ReadAll(r.Body)
				var in struct{ Base string }
				_ = json.Unmarshal(b, &in)
				f.events = append(f.events, "retarget "+strconv.Itoa(n))
				if c := f.refuse[n]; c != 0 {
					w.WriteHeader(c)
					fmt.Fprint(w, `{"message":"Validation Failed"}`)
					return
				}
				if !f.noop[n] {
					f.base[n] = in.Base
				}
				fmt.Fprint(w, `{}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "open", "base": map[string]any{"ref": f.base[n]}})
		default:
			t.Errorf("the fake GitHub was asked for %s %q, which it does not serve", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	was := apiBase
	apiBase = s.URL
	t.Cleanup(func() { apiBase = was })
	return f
}

func (f *stackFake) deleted() bool {
	for _, e := range f.events {
		if e == "delete" {
			return true
		}
	}
	return false
}

func TestNoDependentsDeletesAsBefore(t *testing.T) {
	f := newStackFake(t)
	code, out, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 0 || !f.deleted() || !strings.Contains(out, "branch a-branch deleted") {
		t.Fatalf("code %d deleted=%v\n%s%s", code, f.deleted(), out, errb)
	}
	if strings.Contains(out, "retargeted") {
		t.Errorf("claimed a retarget with nothing stacked:\n%s", out)
	}
}

func TestStackedPullRequestsAreRetargetedVerifiedThenTheBranchGoes(t *testing.T) {
	f := newStackFake(t, 7, 9)
	code, out, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	for _, n := range []int{7, 9} {
		if f.base[n] != "main" {
			t.Errorf("#%d base is %q", n, f.base[n])
		}
		if !strings.Contains(out, fmt.Sprintf("go-gitsafe/gitsafe#%d was stacked on a-branch: retargeted to main (verified)", n)) {
			t.Errorf("#%d not named:\n%s", n, out)
		}
	}
	got := strings.Join(f.events, ",")
	if got != "list page 1,retarget 7,retarget 9,delete" {
		t.Errorf("order: %s", got)
	}
}

func TestARefusedRetargetKeepsTheBranchAndSaysTheMergeHappened(t *testing.T) {
	f := newStackFake(t, 7, 9)
	f.refuse[7] = http.StatusUnprocessableEntity
	code, out, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != exitRetargetFailed {
		t.Fatalf("exit %d, want %d", code, exitRetargetFailed)
	}
	if f.deleted() {
		t.Error("the branch was deleted over a pull request that was not retargeted")
	}
	if !strings.Contains(out, "merged") || !strings.Contains(errb, "IS merged") ||
		!strings.Contains(errb, "go-gitsafe/gitsafe#7") || !strings.Contains(errb, "KEPT") {
		t.Errorf("not clear enough:\nstdout %s\nstderr %s", out, errb)
	}
	if f.base[9] != "main" || !strings.Contains(out, "#9 was stacked") {
		t.Errorf("the other dependent should still be moved:\n%s", out)
	}
}

func TestAnAcceptedPatchThatChangedNothingIsNotARetarget(t *testing.T) {
	f := newStackFake(t, 7)
	f.noop[7] = true
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != exitRetargetFailed || f.deleted() || !strings.Contains(errb, `still "a-branch"`) {
		t.Fatalf("code %d deleted=%v: %s", code, f.deleted(), errb)
	}
}

func TestAFailedListingKeepsTheBranch(t *testing.T) {
	f := newStackFake(t)
	f.listErr = true
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != exitRetargetFailed || f.deleted() || !strings.Contains(errb, "could not list") {
		t.Fatalf("code %d deleted=%v: %s", code, f.deleted(), errb)
	}
}

func TestEveryPageOfDependentsIsRead(t *testing.T) {
	var nums []int
	for i := 0; i < 101; i++ {
		nums = append(nums, 1000+i)
	}
	f := newStackFake(t, nums...)
	code, _, errb := try(t, "go-gitsafe/gitsafe", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if f.base[1100] != "main" || f.base[1000] != "main" {
		t.Errorf("the 101st (page 2) dependent was missed: %q", f.base[1100])
	}
	if !strings.Contains(strings.Join(f.events, ","), "list page 2") {
		t.Errorf("page 2 never asked: %v", f.events[:3])
	}
}

func TestAnUnknownNewBaseKeepsTheBranch(t *testing.T) {
	newStackFake(t, 7)
	var out, errb bytes.Buffer
	if retargetStacked("x", "go-gitsafe/gitsafe", "a-branch", "", &out, &errb) {
		t.Error("with no base to move to, the branch must stay")
	}
	if !strings.Contains(errb.String(), "#7") {
		t.Errorf("not named: %s", errb.String())
	}
}

func TestACanceledReadBackIsNamed(t *testing.T) {
	// the pull request closed itself between the PATCH and the read-back
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprint(w, `{"state":"closed","base":{"ref":"main"}}`)
	}))
	defer s.Close()
	was := apiBase
	apiBase = s.URL
	defer func() { apiBase = was }()
	if err := retarget("x", "o/r", 7, "main"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("err = %v", err)
	}
	s.Close()
	if err := retarget("x", "o/r", 7, "main"); err == nil {
		t.Error("a dead server is not a retarget")
	}
}

func TestRetargetReadBackFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			fmt.Fprint(w, `{}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s.Close()
	was := apiBase
	apiBase = s.URL
	defer func() { apiBase = was }()
	if err := retarget("x", "o/r", 7, "main"); err == nil || !strings.Contains(err.Error(), "reading it back") {
		t.Errorf("err = %v", err)
	}
}

// Driven the way a person drives it: through the cobra command.
func TestStackedRetargetThroughCobra(t *testing.T) {
	f := newStackFake(t, 7)
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"--token-file", withToken(t), "go-gitsafe/gitsafe", "1"})
	if err := c.Execute(); err != nil {
		t.Fatalf("%v: %s", err, errb.String())
	}
	if f.base[7] != "main" || !f.deleted() {
		t.Errorf("base %q deleted %v", f.base[7], f.deleted())
	}

	// and the failure is a non-zero status through the same door
	f2 := newStackFake(t, 7)
	f2.refuse[7] = http.StatusForbidden
	out.Reset()
	errb.Reset()
	c = RootCmd()
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs([]string{"--token-file", withToken(t), "go-gitsafe/gitsafe", "1"})
	err := c.Execute()
	if st, ok := err.(cliflags.Status); !ok || int(st) != exitRetargetFailed {
		t.Errorf("err = %#v, want status %d", err, exitRetargetFailed)
	}
}

func TestHelpDescribesTheRetarget(t *testing.T) {
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"--help"})
	_ = c.Execute()
	if !strings.Contains(out.String(), "retargeting every open pull request") {
		t.Errorf("help is silent about it:\n%s", out.String())
	}
}
