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

	"github.com/go-gitsafe/gitsafe/glauth"
)

// Assembled, not written whole, so nothing scanning this repository finds a
// credential-shaped string. Routable shape, the one that leaked.
var fakeToken = "gl" + "pat-" + strings.Repeat("Ab0_", 9) + ".01.0w17y347e"
var otherSecret = "gh" + "p_" + strings.Repeat("Z", 36)

type fake struct {
	statuses []string // successive pipeline statuses, for -watch
	calls    int
	noPipe   bool
	mrPath   bool
}

func (f *fake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(glauth.HeaderName) != fakeToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p := r.URL.EscapedPath()
		switch {
		case strings.HasSuffix(p, "/merge_requests/1/pipelines"):
			f.mrPath = true
			fallthrough
		case strings.HasSuffix(p, "/pipelines"):
			if f.noPipe {
				w.Write([]byte(`[]`))
				return
			}
			st := f.statuses[min(f.calls, len(f.statuses)-1)]
			f.calls++
			w.Write([]byte(`[{"id":42,"status":"` + st + `","ref":"main","sha":"0123456789abcdef","source":"push","web_url":"https://gl/p/-/pipelines/42"}]`))
		case strings.HasSuffix(p, "/pipelines/42/jobs"):
			w.Write([]byte(`[{"id":7,"name":"build","stage":"build","status":"success","duration":12},
			                 {"id":8,"name":"publish","stage":"publish","status":"failed","duration":3,"web_url":"https://gl/p/-/jobs/8"}]`))
		case strings.HasSuffix(p, "/jobs/8/trace"):
			w.Write([]byte("section_start:1:step_script\r\x1b[0K\x1b[32;1m$ curl --upload-file x\x1b[0;m\n" +
				"curl: (22) The requested URL returned error: 400\n" +
				"debug: token=" + fakeToken + " other=" + otherSecret + "\n" +
				"section_end:2:step_script\r\x1b[0K\n"))
		default:
			t.Errorf("unexpected request %s", p)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func setup(t *testing.T, f *fake) (args []string) {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	newClient = func(host, tok string) *glauth.Client { return glauth.New(srv.URL, tok) }
	sleep = func(time.Duration) {}
	dir := t.TempDir()
	homeDir = func() (string, error) { return dir, nil }
	file := filepath.Join(dir, "tok")
	os.WriteFile(file, []byte(fakeToken+"\n"), 0o600)
	return []string{"-host", "gl.example", "-project", "group/project", "-f", file}
}

func runGlci(t *testing.T, args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := run(args, t.TempDir(), &out, &errb)
	all := out.String() + errb.String()
	if strings.Contains(all, "Ab0_") || strings.Contains(all, "0w17y347e") || strings.Contains(all, otherSecret) {
		t.Errorf("a secret reached the output:\n%s", all)
	}
	return code, all
}

func TestSuccess(t *testing.T) {
	args := setup(t, &fake{statuses: []string{"success"}})
	code, out := runGlci(t, append(args, "main")...)
	if code != exitSuccess || !strings.Contains(out, "pipeline 42 SUCCESS") {
		t.Errorf("code %d\n%s", code, out)
	}
}

// TestFailureShowsTheMaskedLog: the failed job's log is shown, cleaned of
// colour codes and section markers, with the token used AND another secret
// masked.
func TestFailureShowsTheMaskedLog(t *testing.T) {
	args := setup(t, &fake{statuses: []string{"failed"}})
	code, out := runGlci(t, append(args, "main")...)
	if code != exitFailed {
		t.Errorf("code %d", code)
	}
	for _, want := range []string{"publish", "returned error: 400", "«REDACTED»"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "section_start") {
		t.Errorf("raw log markup in output:\n%q", out)
	}
}

// TestNoPipelineIsItsOwnStatus: silence must not read as success.
func TestNoPipelineIsItsOwnStatus(t *testing.T) {
	args := setup(t, &fake{noPipe: true})
	code, out := runGlci(t, append(args, "main")...)
	if code != exitNoPipeline || !strings.Contains(out, "nothing passed") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestUnfinishedWithoutWatch(t *testing.T) {
	args := setup(t, &fake{statuses: []string{"running"}})
	if code, _ := runGlci(t, append(args, "main")...); code != exitUnfinished {
		t.Errorf("code %d", code)
	}
	args = setup(t, &fake{statuses: []string{"manual"}})
	if code, _ := runGlci(t, append(args, "main")...); code != exitUnfinished {
		t.Errorf("manual counted as finished: %d", code)
	}
}

func TestWatchWaitsForTheEnd(t *testing.T) {
	f := &fake{statuses: []string{"pending", "running", "success"}}
	args := setup(t, f)
	code, _ := runGlci(t, append(args, "-watch", "main")...)
	if code != exitSuccess || f.calls != 3 {
		t.Errorf("code %d after %d polls", code, f.calls)
	}
}

func TestMergeRequest(t *testing.T) {
	f := &fake{statuses: []string{"success"}}
	args := setup(t, f)
	if code, _ := runGlci(t, append(args, "!1")...); code != exitSuccess || !f.mrPath {
		t.Errorf("code %d, MR endpoint used: %v", code, f.mrPath)
	}
	if code, _ := runGlci(t, append(args, "!x")...); code != exitUsage {
		t.Errorf("a bad MR number was accepted: %d", code)
	}
}

func TestRefusedToken(t *testing.T) {
	args := setup(t, &fake{statuses: []string{"success"}})
	os.WriteFile(args[5], []byte("gl"+"pat-"+strings.Repeat("Q", 20)), 0o600)
	code, out := runGlci(t, append(args, "main")...)
	if code != exitUsage || !strings.Contains(out, "401") || strings.Contains(out, strings.Repeat("Q", 20)) {
		t.Errorf("code %d\n%s", code, out)
	}
}
