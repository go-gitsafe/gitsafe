package amendmerged

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// run executes git in dir and fails the test on error.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func commit(t *testing.T, dir, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(subject), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "f.txt")
	run(t, dir, "commit", "-q", "-m", subject)
}

// clone builds a bare origin with one commit on main and a clone of it.
func clone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")
	run(t, root, "init", "-q", "--bare", "-b", "main", origin)
	run(t, root, "init", "-q", "-b", "main", work)
	run(t, work, "remote", "add", "origin", origin)
	commit(t, work, "merged change")
	run(t, work, "push", "-q", "origin", "main")
	run(t, work, "fetch", "-q", "origin")
	return work
}

const amend = "git commit --amend --no-edit"

func TestRefusesAnAmendOfAMergedHead(t *testing.T) {
	w := clone(t)
	f := Check(amend, w, "/home/x")
	if !f.Found() {
		t.Fatal("amend of a commit on origin/main was allowed")
	}
	if f.Subject != "merged change" || f.Default != "main" || f.Commit == "" {
		t.Errorf("finding does not name the commit: %+v", f)
	}
	for _, want := range []string{f.Commit, "merged change", "already on main"} {
		if !strings.Contains(f.Why(), want) {
			t.Errorf("Why() lacks %q: %s", want, f.Why())
		}
	}
	for _, want := range []string{"plain `git commit`", "git log -1", Escape} {
		if !strings.Contains(Advice, want) {
			t.Errorf("Advice lacks %q", want)
		}
	}
}

func TestRefusesWhenHiddenInAChain(t *testing.T) {
	w := clone(t)
	for _, cmd := range []string{
		"go mod tidy && git add -A && " + amend,
		"git add -A; " + amend,
		"true || " + amend,
		"git -C " + w + " commit -a --amend -m x",
		"cd " + w + " && git commit --amend",
		"git commit --amend -m \"it said (x)\"",
	} {
		cwd := w
		if strings.HasPrefix(cmd, "git -C") || strings.HasPrefix(cmd, "cd ") {
			cwd = t.TempDir() // the command names the clone, the cwd does not
		}
		if !Check(cmd, cwd, "/home/x").Found() {
			t.Errorf("not caught: %s", cmd)
		}
	}
}

func TestResolvesCdRelativeAndTilde(t *testing.T) {
	w := clone(t)
	parent, name := filepath.Dir(w), filepath.Base(w)
	if !Check("cd "+name+" && "+amend, parent, "/h").Found() {
		t.Error("relative cd not followed")
	}
	if !Check("cd ~/"+name+" && "+amend, "/", parent).Found() {
		t.Error("cd ~/x not followed")
	}
	// cd away: the amend runs elsewhere, in a non-repository.
	if Check("cd "+name+" && cd "+t.TempDir()+" && "+amend, parent, "/h").Found() {
		t.Error("a second cd to a non-repository was still judged against the first")
	}
}

func TestAllowsAnUnpushedTopicCommit(t *testing.T) {
	w := clone(t)
	run(t, w, "checkout", "-q", "-b", "topic")
	commit(t, w, "mine, unpushed")
	if Check(amend, w, "/h").Found() {
		t.Error("amend of an unpushed topic-branch commit was refused")
	}
}

func TestAllowsAPushedButUnmergedTopicCommit(t *testing.T) {
	w := clone(t)
	run(t, w, "checkout", "-q", "-b", "topic")
	commit(t, w, "mine, pushed")
	run(t, w, "push", "-q", "origin", "topic")
	run(t, w, "fetch", "-q", "origin")
	if Check(amend, w, "/h").Found() {
		t.Error("amend of a pushed, unmerged topic commit was refused")
	}
}

// Once the topic is merged (fast-forward here) its HEAD is on main: refused.
func TestRefusesATopicCommitOnceMerged(t *testing.T) {
	w := clone(t)
	run(t, w, "checkout", "-q", "-b", "topic")
	commit(t, w, "mine, merged")
	run(t, w, "push", "-q", "origin", "topic:main")
	run(t, w, "fetch", "-q", "origin")
	if f := Check(amend, w, "/h"); !f.Found() || f.Subject != "mine, merged" {
		t.Errorf("got %+v", f)
	}
}

func TestAllowsWhatIsNotAnAmend(t *testing.T) {
	w := clone(t)
	for _, cmd := range []string{
		"git commit -m x",
		"git commit -F msg.txt",
		`git commit -m "use --amend later"`,
		`git commit -m 'git commit --amend'`,
		"git commit -m x -- --amend",
		"echo git commit --amend",
		"git log --amend",
		"git commit -m x && " + amend, // HEAD is the new commit by then
		"git reset --soft origin/main && " + amend,
		"git checkout -b t && " + amend,
		"",
	} {
		if Check(cmd, w, "/h").Found() {
			t.Errorf("refused: %s", cmd)
		}
	}
}

func TestEscapeOpensIt(t *testing.T) {
	w := clone(t)
	if Check(Escape+"=1 "+amend, w, "/h").Found() {
		t.Error("the escape did not open it")
	}
	if !Check(Escape+"=0 "+amend, w, "/h").Found() {
		t.Error("=0 must not open it")
	}
}

// Every case where git cannot answer is an allow, never a refusal.
func TestAllowsWhenItCannotCheck(t *testing.T) {
	t.Run("not a repository", func(t *testing.T) {
		if Check(amend, t.TempDir(), "/h").Found() {
			t.Error("refused outside a repository")
		}
	})
	t.Run("missing directory", func(t *testing.T) {
		if Check(amend, "/nonexistent/for/sure", "/h").Found() {
			t.Error("refused in a missing directory")
		}
	})
	t.Run("empty cwd", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if Check(amend, "", "/h").Found() {
			t.Error("refused with no cwd in a non-repository")
		}
	})
	t.Run("no remote", func(t *testing.T) {
		d := t.TempDir()
		run(t, d, "init", "-q", "-b", "main")
		commit(t, d, "local only")
		if Check(amend, d, "/h").Found() {
			t.Error("refused with no origin")
		}
	})
	t.Run("origin not fetched", func(t *testing.T) {
		w := clone(t)
		run(t, w, "update-ref", "-d", "refs/remotes/origin/main")
		if Check(amend, w, "/h").Found() {
			t.Error("refused with no origin/main")
		}
	})
	t.Run("unborn HEAD", func(t *testing.T) {
		w := clone(t)
		run(t, w, "checkout", "-q", "--orphan", "fresh")
		if Check(amend, w, "/h").Found() {
			t.Error("refused with an unborn HEAD")
		}
	})
}

// origin/HEAD, when set, names the default branch; a repo whose default is not
// main or master is still judged.
func TestHonoursOriginHEAD(t *testing.T) {
	w := clone(t)
	run(t, w, "branch", "-q", "trunk", "origin/main")
	run(t, w, "push", "-q", "origin", "trunk")
	run(t, w, "fetch", "-q", "origin")
	run(t, w, "remote", "set-head", "origin", "trunk")
	run(t, w, "checkout", "-q", "trunk")
	if f := Check(amend, w, "/h"); !f.Found() || f.Default != "trunk" {
		t.Errorf("got %+v", f)
	}
}

func TestSplitWords(t *testing.T) {
	for in, want := range map[string]string{
		`git commit -m "a b" --amend`: "git|commit|-m|a b|--amend",
		`git commit -m 'x --amend y'`: "git|commit|-m|x --amend y",
		`a\ b "" c`:                   "a b||c",
		`git "commit"  --amend`:       "git|commit|--amend",
		`unterminated "quote --amend`: "unterminated|quote --amend",
		`x\`:                          `x\`,
	} {
		if got := strings.Join(splitWords(in), "|"); got != want {
			t.Errorf("splitWords(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHelpers(t *testing.T) {
	if got := resolve("/a", "/h", "$HOME"); got != "/h" {
		t.Error(got)
	}
	if got := resolve("/a", "/h", "$HOME/x"); got != "/h/x" {
		t.Error(got)
	}
	if got := resolve("/a", "/h", "~"); got != "/h" {
		t.Error(got)
	}
	if got := resolve("/a", "/h", "/abs"); got != "/abs" {
		t.Error(got)
	}
	if got := base("/usr/bin/git"); got != "git" {
		t.Error(got)
	}
	if _, sub, _ := subcommand([]string{"-c", "x=y", "--git-dir", "d", "commit"}, "/", "/h"); sub != "commit" {
		t.Error(sub)
	}
	if _, sub, _ := subcommand([]string{"-p"}, "/", "/h"); sub != "" {
		t.Error(sub)
	}
	if cmd := "cd"; Check(cmd+" && "+amend, "/nonexistent", "/nonexistent2").Found() {
		t.Error("bare cd")
	}
}

// The injected inspector is what decides; the parsing alone never refuses.
func TestParsingAloneNeverRefuses(t *testing.T) {
	never := func(string) (string, string, string, bool) { return "", "", "", false }
	if check(amend, "/x", "/h", never).Found() {
		t.Error("refused without an inspector saying so")
	}
}

func FuzzCheck(f *testing.F) {
	for _, s := range []string{amend, "cd x && git commit --amend", `git -C "a b" commit --amend`, "git commit -m 'x", "\\", "((", "$(git commit --amend)"} {
		f.Add(s)
	}
	never := func(string) (string, string, string, bool) { return "", "", "", false }
	f.Fuzz(func(t *testing.T, s string) {
		check(s, "/x", "/h", never) // must not panic
		splitWords(s)
	})
}
