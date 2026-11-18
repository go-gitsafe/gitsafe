package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// An amend of a commit already on origin/main is refused through the whole hook,
// naming the commit and the way out; the same amend on an unpushed topic commit,
// and a plain commit, pass; and the escape typed before the command opens it.
func TestAmendOfAMergedHead(t *testing.T) {
	// The repositories live under the test's temp dir, which the volatile rule
	// would refuse a commit in; this test is about the amend rule.
	t.Setenv("GITSAFE_ALLOW_VOLATILE", "1")
	root := t.TempDir()
	origin, work := filepath.Join(root, "o.git"), filepath.Join(root, "w")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	git(t, root, "init", "-q", "-b", "main", work)
	git(t, work, "remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "f")
	git(t, work, "commit", "-q", "-m", "previous PR merge")
	git(t, work, "push", "-q", "origin", "main")
	git(t, work, "fetch", "-q", "origin")

	in := func(cmd string) string {
		return `{"cwd":"` + work + `","tool_input":{"command":"` + cmd + `"}}`
	}
	out, _ := drive(t, in("go mod tidy && git add -A && git commit --amend --no-edit"))
	for _, want := range []string{`"deny"`, "previous PR merge", "already on main", "git log -1", "GITSAFE_ALLOW_AMEND_MERGED=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	for name, cmd := range map[string]string{
		"plain commit": "git commit -m x",
		"escape":       "GITSAFE_ALLOW_AMEND_MERGED=1 git commit --amend --no-edit",
	} {
		if out, _ := drive(t, in(cmd)); out != "" {
			t.Errorf("%s was refused:\n%s", name, out)
		}
	}
	git(t, work, "checkout", "-q", "-b", "topic")
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "commit", "-q", "-a", "-m", "mine")
	if out, _ := drive(t, in("git commit --amend --no-edit")); out != "" {
		t.Errorf("amend of an unpushed topic commit was refused:\n%s", out)
	}
}
