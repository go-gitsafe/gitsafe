package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-gitsafe/gitsafe/ghauth"
)

// serveVia points the one HTTP seam at a handler, so run() can be exercised
// end to end without an API and without changing the URLs it builds.
func serveVia(t *testing.T, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := httpDo
	t.Cleanup(func() { httpDo = old })
	httpDo = func(req *http.Request) (*http.Response, error) {
		u := *req.URL
		u.Scheme, u.Host = "http", strings.TrimPrefix(srv.URL, "http://")
		r2, err := http.NewRequest(req.Method, u.String(), nil)
		if err != nil {
			return nil, err
		}
		r2.Header = req.Header
		return http.DefaultClient.Do(r2)
	}
}

// run() end to end: the credential comes from the FILE, never an argument, and
// nothing it runs carries it.
func TestRunReadsTheCredentialFromTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Not a real token; the shape is what matters to the redactor.
	tok := "gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz"
	if err := os.WriteFile(filepath.Join(home, ghauth.DefaultFile), []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{runners: []runner{{ID: 9, Name: "z1", Status: "online",
		Labels: []struct {
			Name string `json:"name"`
		}{{Name: "self-hosted"}, {Name: "S390X"}}}}}
	serveVia(t, api.handler(t))
	calls := recordExec(t)
	old := sleepStep
	sleepStep = time.Millisecond
	t.Cleanup(func() { sleepStep = old })

	var out, errb bytes.Buffer
	// z1 is ONLINE, so this refuses — and that path still proves the point:
	// the credential was read from the file and the API answered with it.
	if code := run([]string{"-host", "me@m", "-name", "z1", "o/r"}, &out, &errb); code != 1 {
		t.Fatalf("code = %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "ONLINE") {
		t.Errorf("stderr: %s", errb.String())
	}
	if len(api.calls) == 0 {
		t.Error("premise: the API was never reached, so no credential was used")
	}
	for _, r := range *calls {
		if strings.Contains(strings.Join(r.argv, " "), "ghp_") {
			t.Error("the credential reached a command line")
		}
	}

	// With the name free and no -name given, it registers and says plainly
	// that it cannot check the labels.
	api.runners = nil
	out.Reset()
	errb.Reset()
	if code := run([]string{"-host", "me@m", "o/r"}, &out, &errb); code != 0 {
		t.Fatalf("code = %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "check its labels") {
		t.Errorf("without a name the labels are unchecked and it must say so: %s", out.String())
	}
}

// -remove takes the machine's side down AND the entry here: config.sh remove
// does not clear the API entry once the two have parted, which is exactly the
// state a destroyed machine leaves behind.
func TestRemoveClearsBothSides(t *testing.T) {
	api := &fakeAPI{runners: []runner{{ID: 11, Name: "z1", Status: "offline"}}}
	serveVia(t, api.handler(t))
	calls := recordExec(t)
	c := &client{token: "t", repo: "o/r"}
	var out, errb bytes.Buffer
	if code := doRemove(c, options{host: "me@m", name: "z1"}, &out, &errb); code != 0 {
		t.Fatalf("code = %d: %s", code, errb.String())
	}
	if len(api.deleted) != 1 || api.deleted[0] != 11 {
		t.Errorf("the API entry was not removed: %v", api.deleted)
	}
	var joined string
	for _, r := range *calls {
		joined += strings.Join(r.argv, " ") + "\n"
	}
	if !strings.Contains(joined, "svc.sh uninstall") {
		t.Errorf("the service was left installed:\n%s", joined)
	}
	if !strings.Contains(joined, "config.sh 'remove'") {
		t.Errorf("the machine's own registration was left behind:\n%s", joined)
	}
}

// An API that refuses is reported as itself, and the message points at the
// scope rather than inviting the reader to print the token and look.
func TestRegistrationTokenRefused(t *testing.T) {
	serveVia(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	c := &client{token: "t", repo: "o/r"}
	if _, err := c.registrationToken(); err == nil {
		t.Fatal("a 403 must be an error")
	} else if !strings.Contains(err.Error(), "scope") {
		t.Errorf("the message should say what to check: %v", err)
	}
}
