package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-gitsafe/gitsafe/redact"
	"github.com/go-gitsafe/gitsafe/secretarg"
)

// regTok is the shape of a runner registration token: 29 bare uppercase
// characters, no issuer prefix. Not a real one.
const regTok = "AABF3JGZDX3P5PMEXLND6TS6FCWO6"

// fakeAPI answers the four calls this command makes.
//
// Guarded by a mutex because the handler runs on the server's goroutines while
// the test reads what it recorded. The first version left it bare and mutated
// it from a goroutine of its own; `go test -race` in CI reported the race and
// the test had looked fine on one machine.
type fakeAPI struct {
	mu      sync.Mutex
	runners []runner
	deleted []int
	calls   []string
	// onlineAfter makes the Nth listing the first to report the runner, so
	// report()'s polling is exercised deterministically rather than by timing.
	onlineAfter int
	lists       int
}

func (f *fakeAPI) snapshotCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeAPI) snapshotDeleted() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.deleted...)
}

func (f *fakeAPI) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

func (f *fakeAPI) serve(t *testing.T) *httptest.Server {
	s := httptest.NewServer(f.handler(t))
	t.Cleanup(s.Close)
	return s
}

func (f *fakeAPI) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") == "" {
			t.Error("the API was called without a credential")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/registration-token"),
			strings.HasSuffix(r.URL.Path, "/remove-token"):
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"token": regTok})
		case strings.HasSuffix(r.URL.Path, "/actions/runners"):
			f.lists++
			rs := f.runners
			// The runner arrives LATE, on the Nth listing: the service starts
			// and it takes a moment to connect. Counted here rather than timed
			// from outside, which was a data race.
			if f.onlineAfter > 0 {
				if f.lists < f.onlineAfter {
					rs = nil
				} else {
					rs = []runner{{ID: 9, Name: "z1", Status: "online",
						Labels: []struct {
							Name string `json:"name"`
						}{{Name: "self-hosted"}, {Name: "Linux"}, {Name: "S390X"}}}}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"runners": rs})
		case r.Method == http.MethodDelete:
			var id int
			fmt.Sscanf(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "%d", &id)
			f.deleted = append(f.deleted, id)
			kept := f.runners[:0]
			for _, rr := range f.runners {
				if rr.ID != id {
					kept = append(kept, rr)
				}
			}
			f.runners = kept
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return mux
}

// recordExec replaces ssh with a recorder, and returns what was asked of it:
// the argv of each call and the stdin each was given.
type recorded struct {
	argv  []string
	stdin string
}

func recordExec(t *testing.T) *[]recorded {
	t.Helper()
	var got []recorded
	old := execCommand
	t.Cleanup(func() { execCommand = old })
	execCommand = func(name string, args ...string) *exec.Cmd {
		// `true` succeeds and reads nothing; the recording is what matters.
		c := exec.Command("/usr/bin/true")
		rec := recorded{argv: append([]string{name}, args...)}
		got = append(got, rec)
		i := len(got) - 1
		c.Stdin = nil
		// Capture whatever the caller sets as stdin, after it sets it.
		t.Cleanup(func() {
			if c.Stdin != nil {
				var b bytes.Buffer
				b.ReadFrom(c.Stdin)
				got[i].stdin = b.String()
			}
		})
		return c
	}
	return &got
}

// THE test. Everything else here is a detail beside it.
//
// The registration token must not appear in any argv, and the command line
// that is built must not be one secretarg would refuse — it is the same
// question this repository asks of everything else, asked of itself.
func TestTheTokenNeverReachesACommandLine(t *testing.T) {
	api := &fakeAPI{runners: []runner{{ID: 7, Name: "z1", Status: "online",
		Labels: []struct {
			Name string `json:"name"`
		}{{Name: "self-hosted"}, {Name: "Linux"}, {Name: "S390X"}}}}}
	srv := api.serve(t)
	calls := recordExec(t)
	old := sleepStep
	sleepStep = time.Millisecond
	t.Cleanup(func() { sleepStep = old })

	c := &client{token: "ghp_notarealtokenatallxxxxxxxxxxxxxxxxxxx", repo: "o/r", base: srv.URL + "/repos/o/r"}
	c.base = srv.URL
	var out, errb bytes.Buffer
	o := options{host: "me@machine", name: "z1", labels: "S390X", group: "Default", work: "_work"}
	// z1 is ONLINE, so registering must refuse rather than take it out.
	if code := doRegister(c, o, &out, &errb); code != 1 {
		t.Fatalf("code = %d; an ONLINE runner of the same name must not be removed silently\n%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "already registered and ONLINE") {
		t.Errorf("stderr: %s", errb.String())
	}

	// Now the real path, with the name free — and the runner arriving LATE,
	// which is what actually happens: the service starts and the runner takes
	// a moment to connect, so report() polls.
	//
	// Its own fake, rather than reaching into the first one. Driving the
	// arrival by mutating a shared fake from a goroutine was a data race,
	// reported by CI's `go test -race` after it had looked fine here; driving
	// it by resetting that fake's counter without the lock would be the same
	// race written more carefully.
	api2 := &fakeAPI{onlineAfter: 2}
	srv2 := api2.serve(t)
	c2 := &client{token: c.token, repo: "o/r", base: srv2.URL}
	out.Reset()
	errb.Reset()
	if code := doRegister(c2, o, &out, &errb); code != 0 {
		t.Fatalf("code = %d\n%s%s", code, out.String(), errb.String())
	}
	if api2.listCount() < 2 {
		t.Error("premise: report() did not have to poll, so the late arrival was not exercised")
	}

	if len(*calls) == 0 {
		t.Fatal("premise: nothing was run, so there is no command line to check")
	}
	for _, r := range *calls {
		line := strings.Join(r.argv, " ")
		if strings.Contains(line, regTok) {
			t.Errorf("THE token is on a command line: %s", line)
		}
		if strings.Contains(line, "--token") {
			t.Errorf("config.sh was given --token, which is the one thing this exists to avoid: %s", line)
		}
		// And the command line this builds must survive the repository's own
		// rule. Asking secretarg here is the point: the two cannot drift.
		if f := secretarg.Check(line); f.Found() {
			t.Errorf("secretarg refuses the command this builds (%s): %s", f.Rule, line)
		}
	}
	if !strings.Contains(out.String(), "S390X") {
		t.Errorf("the labels must be reported, because wrong ones look like nothing: %s", out.String())
	}
}

// The PTY and the echo are not decoration: a pipe fails with "Cannot read keys
// … console input has been redirected", and an echoing terminal reflects the
// token into whatever records the session.
func TestConfigureAsksForATerminalWithTheEchoOff(t *testing.T) {
	calls := recordExec(t)
	c := &client{token: "t", repo: "o/r"}
	var out, errb bytes.Buffer
	if code := c.sshConfigure("me@machine", []string{"--url", "https://github.com/o/r"}, regTok, &out, &errb); code != 0 {
		t.Fatalf("code = %d: %s", code, errb.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("%d call(s)", len(*calls))
	}
	argv := (*calls)[0].argv
	line := strings.Join(argv, " ")
	if argv[0] != "ssh" || !contains(argv, "-tt") {
		t.Errorf("no PTY was asked for: %s", line)
	}
	if !strings.Contains(line, "stty -echo") {
		t.Errorf("the echo was left on: %s", line)
	}
	if !strings.Contains(line, "stty echo") {
		t.Errorf("the echo is never put back: %s", line)
	}
}

// A label with a space in it would otherwise become two flags.
func TestQuoteAll(t *testing.T) {
	got := strings.Join(quoteAll([]string{"--labels", "a b", "it's"}), " ")
	if got != `'--labels' 'a b' 'it'\''s'` {
		t.Errorf("got %s", got)
	}
}

// Everything printed goes through the redactor, so a token that does reach the
// output is not published by this command.
func TestOutputIsRedacted(t *testing.T) {
	var buf bytes.Buffer
	w := &redactWriter{w: &buf, r: redact.New("ghp_0123456789abcdefghijklmnopqrstuvwxyz")}
	n, err := w.Write([]byte("before ghp_0123456789abcdefghijklmnopqrstuvwxyz after"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 53 {
		t.Errorf("Write must report the bytes it was GIVEN, not the bytes it wrote: %d", n)
	}
	if strings.Contains(buf.String(), "0123456789abcdef") {
		t.Errorf("the secret went out: %s", buf.String())
	}
}

// Usage and the refusals that come before anything is contacted.
func TestRunRefusesBeforeItTouchesAnything(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
		says string
	}{
		{"no repo", []string{"-host", "h"}, 2, "usage"},
		{"no host", []string{"o/r"}, 2, "-host is required"},
		{"repo is not owner/slash", []string{"-host", "h", "notarepo"}, 2, "is not owner/repo"},
		{"a flag it does not know", []string{"-nope"}, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb); code != tc.want {
				t.Errorf("code = %d, want %d: %s", code, tc.want, errb.String())
			}
			if tc.says != "" && !strings.Contains(errb.String(), tc.says) {
				t.Errorf("stderr: %s", errb.String())
			}
		})
	}
}

// A token file that is not there is a plain refusal, and the error must not
// carry a path a reader would then go and cat.
func TestRunWithoutACredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"-host", "h", "o/r"}, &out, &errb); code != 1 {
		t.Errorf("code = %d: %s", code, errb.String())
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
