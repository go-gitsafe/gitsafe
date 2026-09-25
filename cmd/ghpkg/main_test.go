package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub answers as GitHub would, and records what was asked of it. The
// DELETE is what matters: a test that cannot see whether it happened cannot
// tell a refusal from a silent success.
type fakeGitHub struct {
	scopes   string
	versions []map[string]any
	deleted  []string
	listCode int
	delCode  int
}

func (f *fakeGitHub) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The token must arrive in a HEADER, never in the URL or the query.
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if strings.Contains(r.URL.String(), "tok") {
			t.Errorf("the token reached the URL: %s", r.URL)
		}
		switch {
		case r.URL.Path == "/user":
			w.Header().Set("X-OAuth-Scopes", f.scopes)
			w.Write([]byte(`{"login":"someone"}`))
		case r.Method == http.MethodDelete:
			f.deleted = append(f.deleted, r.URL.Path)
			w.WriteHeader(orDefault(f.delCode, http.StatusNoContent))
		default:
			if f.listCode != 0 && f.listCode != http.StatusOK {
				w.WriteHeader(f.listCode)
				return
			}
			if r.URL.Query().Get("page") != "1" {
				w.Write([]byte(`[]`))
				return
			}
			_ = json.NewEncoder(w).Encode(f.versions)
		}
	}))
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
	return srv.URL
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func ver(id int, tags ...string) map[string]any {
	return map[string]any{
		"id": id, "name": "sha256:x", "created_at": "2026-09-25T10:00:00Z",
		"metadata": map[string]any{"container": map[string]any{"tags": tags}},
	}
}

func tokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Without --yes NOTHING is deleted, and what would have been is printed. A
// deleted package version cannot be restored, and the registry is shared.
func TestDeleteWithoutYesDoesNothing(t *testing.T) {
	f := &fakeGitHub{scopes: "delete:packages, write:packages", versions: []map[string]any{ver(1, "2.1.91")}}
	f.start(t)
	var out, errb bytes.Buffer
	code := run([]string{"delete", "go-pkgx", "packages/x", "2.1.91", "--token", tokenFile(t)}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if len(f.deleted) != 0 {
		t.Fatalf("it deleted %v without --yes", f.deleted)
	}
	if !strings.Contains(out.String(), "would delete") || !strings.Contains(out.String(), "nothing done") {
		t.Errorf("output:\n%s", out.String())
	}
}

// With --yes it deletes the version the tag names, and says which.
func TestDeleteWithYes(t *testing.T) {
	f := &fakeGitHub{scopes: "delete:packages, write:packages",
		versions: []map[string]any{ver(1, "3.2.0"), ver(7, "2.1.91")}}
	f.start(t)
	var out, errb bytes.Buffer
	code := run([]string{"delete", "go-pkgx", "packages/x", "2.1.91", "--yes", "--token", tokenFile(t)}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d: %s %s", code, out.String(), errb.String())
	}
	if len(f.deleted) != 1 || !strings.HasSuffix(f.deleted[0], "/versions/7") {
		t.Errorf("deleted %v, want version 7", f.deleted)
	}
}

// A container version is a MANIFEST, and several tags can point at one.
// Deleting 2.1.91 can take 2.1 and v2 with it — said BEFORE the irreversible
// step, not after.
func TestDeleteNamesTheOtherTagsItWouldRemove(t *testing.T) {
	f := &fakeGitHub{scopes: "delete:packages", versions: []map[string]any{ver(7, "2.1.91", "2.1", "v2")}}
	f.start(t)
	var out, errb bytes.Buffer
	run([]string{"delete", "go-pkgx", "packages/x", "2.1.91", "--token", tokenFile(t)}, &out, &errb)
	if !strings.Contains(out.String(), "also carries") ||
		!strings.Contains(out.String(), "2.1") || !strings.Contains(out.String(), "v2") {
		t.Errorf("the other tags were not named:\n%s", out.String())
	}
}

// A tag that names nothing, and one that names two things: neither is
// something to guess about.
func TestDeleteRefusesAnAmbiguousOrAbsentTag(t *testing.T) {
	for _, tc := range []struct {
		name string
		vs   []map[string]any
		want string
	}{
		{"absent", []map[string]any{ver(1, "3.2.0")}, "no version"},
		{"two of them", []map[string]any{ver(1, "2.1.91"), ver(2, "2.1.91")}, "not something to guess"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeGitHub{scopes: "delete:packages", versions: tc.vs}
			f.start(t)
			var out, errb bytes.Buffer
			if code := run([]string{"delete", "go-pkgx", "packages/x", "2.1.91", "--yes", "--token", tokenFile(t)}, &out, &errb); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if len(f.deleted) != 0 {
				t.Fatalf("it deleted %v anyway", f.deleted)
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("it said %q", errb.String())
			}
		})
	}
}

// A missing permission is reported as what it IS, before any request that
// would answer 403 from an endpoint nobody has heard of. And the hierarchy is
// counted: write:packages can read them.
func TestScopesAreCheckedBeforeAsking(t *testing.T) {
	f := &fakeGitHub{scopes: "write:packages", versions: []map[string]any{ver(7, "2.1.91")}}
	f.start(t)
	var out, errb bytes.Buffer
	if code := run([]string{"delete", "go-pkgx", "packages/x", "2.1.91", "--yes", "--token", tokenFile(t)}, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if len(f.deleted) != 0 {
		t.Fatal("it deleted without delete:packages")
	}
	if !strings.Contains(errb.String(), "delete:packages") || strings.Contains(errb.String(), "read:packages") {
		t.Errorf("it said %q — write:packages covers read:packages", errb.String())
	}
}

// list prints every version and counts them.
func TestList(t *testing.T) {
	f := &fakeGitHub{scopes: "read:packages", versions: []map[string]any{ver(1, "3.2.0"), ver(7)}}
	f.start(t)
	var out, errb bytes.Buffer
	if code := runList(tokenFile(t), "go-pkgx", "packages/x", &out, &errb); code != 0 {
		t.Fatalf("exit = %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "3.2.0") || !strings.Contains(out.String(), "(untagged)") ||
		!strings.Contains(out.String(), "2 version(s)") {
		t.Errorf("output:\n%s", out.String())
	}
}

// An error from GitHub is reported, not swallowed into an empty list — which
// would read as "the tag is not there" and send somebody looking in the wrong
// place.
func TestListReportsAnErrorRatherThanNothing(t *testing.T) {
	f := &fakeGitHub{scopes: "read:packages", listCode: http.StatusForbidden}
	f.start(t)
	var out, errb bytes.Buffer
	if code := runList(tokenFile(t), "go-pkgx", "packages/x", &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "403") {
		t.Errorf("it said %q", errb.String())
	}
}

// A delete GitHub refuses is an error, not a success with nothing done.
func TestDeleteReportsARefusal(t *testing.T) {
	f := &fakeGitHub{scopes: "delete:packages", versions: []map[string]any{ver(7, "2.1.91")}, delCode: http.StatusForbidden}
	f.start(t)
	var out, errb bytes.Buffer
	if code := run([]string{"delete", "go-pkgx", "packages/x", "2.1.91", "--yes", "--token", tokenFile(t)}, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "403") {
		t.Errorf("it said %q", errb.String())
	}
}

// A credential that cannot be read stops everything: the alternative is asking
// GitHub who nobody is.
func TestAMissingTokenStops(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"list", "go-pkgx", "packages/x", "--token", "/nope/nothing"}, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "cannot read") {
		t.Errorf("it said %q", errb.String())
	}
}

// Usage mistakes are usage mistakes, not empty successes.
func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"list"},
		{"list", "only-one"},
		{"delete", "go-pkgx", "packages/x"},
		{"frobnicate", "a", "b"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
}
