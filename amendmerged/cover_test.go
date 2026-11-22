package amendmerged

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeGit puts a `git` shell script first on PATH, so the three outcomes the
// real git cannot be made to give on demand are reachable: a log that fails, a
// log that answers nothing, and a git that is not there at all.
func fakeGit(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if script != "" {
		if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// Every doubt allows: a log that fails, or answers nothing, is no verdict.
func TestALogThatFailsOrIsEmptyAllowsTheAmend(t *testing.T) {
	// $3 is the subcommand: git -C <dir> <sub> ...
	const common = `case "$3" in symbolic-ref) exit 1;; rev-parse|merge-base) exit 0;; esac
`
	for name, script := range map[string]string{
		"log fails":      common + "exit 3\n",
		"log says empty": common + "exit 0\n",
	} {
		fakeGit(t, script)
		if c, _, _, ok := gitInspect("/x"); ok {
			t.Errorf("%s: gitInspect answered %q", name, c)
		}
	}
}

// With no git to run, the call answers -1 and nothing is refused.
func TestAMissingGitIsCannotCheck(t *testing.T) {
	fakeGit(t, "")
	if out, code := git("/x", "status"); out != "" || code != -1 {
		t.Errorf("git = %q, %d", out, code)
	}
	if _, _, ok := defaultRef("/x"); ok {
		t.Error("defaultRef found a branch without git")
	}
}

// origin/master is the fallback when origin/main does not exist.
func TestOriginMasterIsTheFallback(t *testing.T) {
	fakeGit(t, `case "$3" in symbolic-ref) exit 1;; rev-parse) case "$6" in origin/master*) exit 0;; *) exit 1;; esac;; esac
exit 1
`)
	ref, name, ok := defaultRef("/x")
	if !ok || ref != "origin/master" || name != "master" {
		t.Errorf("defaultRef = %q, %q, %v", ref, name, ok)
	}
}

// A statement that is only assignments has no command; the next one is read.
func TestAStatementOfOnlyAssignmentsIsSkipped(t *testing.T) {
	called := false
	f := check("FOO=1; git commit --amend", "/r", "/h", func(dir string) (string, string, string, bool) {
		called = true
		return "abc", "s", "main", true
	})
	if !called || !f.Found() {
		t.Errorf("the amend after a bare assignment was not judged: %+v", f)
	}
}

// A backslash inside double quotes keeps the next character, quote included.
func TestABackslashInDoubleQuotes(t *testing.T) {
	got := splitWords(`git commit -m "say \"hi\"" --amend`)
	want := []string{"git", "commit", "-m", `say "hi"`, "--amend"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitWords = %q, want %q", got, want)
	}
}
