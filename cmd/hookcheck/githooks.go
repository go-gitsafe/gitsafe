package main

// The git hooks, judged the same way as the Bash guards: by what they ANSWER.
//
//	hookcheck -git            # report on the ACTIVE hooks
//	hookcheck -git -install   # recopy the ones that answer wrongly, then report
//
// # Why this exists
//
// ~/.config/git/hooks/pre-push and post-checkout are COPIES of the
// git-pre-push-guard and git-post-checkout-guard binaries, and git runs only
// those two file names. On 2026-11-22 the active copies were found at v0.45.0
// while the binaries beside them were v0.59.0, and nothing on the machine had
// ever compared the two: a stale guard WORKS, it only judges with older rules.
//
// ⛔ The same doctrine as the Bash guards (see the top of main.go, and PR #27):
// this reads no version as the VERDICT. It runs the file git would run — the
// one NAMED pre-push / post-checkout in the hooks directory, never a homonym
// elsewhere — against a fixture and records whether it refused. The version
// the file was built from is printed as context only.
//
// # What a probe can and cannot see
//
// A probe distinguishes two builds only where their rules differ. The pre-push
// guard's rules are: a credential in the URL, a write to the default branch, a
// blob of bigblob.Limit or more in any commit pushed (since v0.40.0), a nested
// repository nobody declared. The guards' code did not change between v0.40.0
// and the release this was written in, so a copy from any of those answers
// exactly as the current one: it is not "behind" in any way a verdict can show,
// and this says so rather than inventing a difference. A copy older than
// v0.40.0 refuses nothing big and goes red here. When a guard gains a rule,
// the rule gets a row in [gitCases] on the day it ships — the same duty the
// Bash corpus has — or this check goes blind to it.
//
// Both halves are asked: a hook that refuses everything is wrong on the rows
// that must pass.

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-gitsafe/gitsafe/bigblob"
)

// gitHookNames are the file names git runs, and the binaries they are copies of.
var gitHookNames = []struct{ file, binary string }{
	{"pre-push", "git-pre-push-guard"},
	{"post-checkout", "git-post-checkout-guard"},
}

const zeros = "0000000000000000000000000000000000000000"

// gitCase is one row: a fixture, how the hook is started, what it owes.
type gitCase struct {
	hook string // "pre-push" or "post-checkout"
	name string
	want string // "refuse" or "allow"
	// fixture is the repository the hook runs in: "small", "big" or "cred".
	fixture string
	args    []string
	// refs is the stdin of a pre-push hook; %HEAD% is replaced by the fixture's
	// tip commit.
	refs string
}

var gitCases = []gitCase{
	{"pre-push", "a credential in the remote URL", "refuse", "small",
		[]string{"origin", "https://" + ghp + "@github.com/go-gitsafe/probe.git"},
		"refs/heads/probe %HEAD% refs/heads/probe " + zeros + "\n"},
	{"pre-push", "a write to main", "refuse", "small",
		[]string{"origin", "https://github.com/go-gitsafe/probe.git"},
		// The remote ref must EXIST (a non-zero old value): creating a branch of
		// that name is not a write to it, and the first version of this row,
		// which used zeros, was allowed by the current guard for that reason.
		"refs/heads/probe %HEAD% refs/heads/main %HEAD%\n"},
	{"pre-push", "a 1 MiB blob in a commit pushed", "refuse", "big",
		[]string{"origin", "https://github.com/go-gitsafe/probe.git"},
		"refs/heads/probe %HEAD% refs/heads/probe " + zeros + "\n"},
	{"pre-push", "an ordinary branch, small files", "allow", "small",
		[]string{"origin", "https://github.com/go-gitsafe/probe.git"},
		"refs/heads/probe %HEAD% refs/heads/probe " + zeros + "\n"},
	{"post-checkout", "a credential in the remote URL", "refuse", "cred",
		[]string{zeros, zeros, "1"}, ""},
	{"post-checkout", "a clean remote", "allow", "small",
		[]string{zeros, zeros, "1"}, ""},
}

// probeRoot is where fixtures are built: durable, and never inside a
// repository. A variable so tests can aim it.
var probeRoot = func() (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "gitsafe-hookcheck"), nil
}

// gitGlobalHooksPath answers `git config --global core.hooksPath`.
// Empty when it is unset (git exits 1) or git cannot run: both mean no global
// hook is guarding.
var gitGlobalHooksPath = func() string {
	out, _ := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
	return strings.TrimSpace(string(out))
}

type gitResult struct {
	c   gitCase
	got string
}

func (r gitResult) wrong() bool { return r.got != r.c.want }

// cleanEnv is the environment a probe runs in: no escape hatch, no user or
// system git configuration that could change what a hook does.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GITSAFE_ALLOW_") || strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
}

func gitIn(dir string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=probe", "-c", "user.email=probe@example.invalid",
		"-c", "commit.gpgsign=false"}, args...)...)
	c.Env = cleanEnv()
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// buildFixture makes one repository and returns its path and tip commit.
//
// The first failure is kept and the rest still run: every step is then
// harmless (a command in a directory that does not exist fails too), and one
// check at the end replaces a branch per step.
func buildFixture(root, kind string) (dir, head string, err error) {
	dir = filepath.Join(root, kind)
	keep := func(e error) {
		if err == nil {
			err = e
		}
	}
	git := func(args ...string) string {
		out, e := gitIn(dir, args...)
		keep(e)
		return out
	}
	size := 2 // a.txt
	if kind == "big" {
		size = bigblob.Limit
	}
	url := "https://github.com/go-gitsafe/probe.git"
	if kind == "cred" {
		url = "https://" + ghp + "@github.com/go-gitsafe/probe.git"
	}
	keep(os.MkdirAll(dir, 0o755))
	git("init", "-q", "-b", "probe")
	keep(os.WriteFile(filepath.Join(dir, "a.txt"), make([]byte, size), 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "probe")
	git("remote", "add", "origin", url)
	head = git("rev-parse", "HEAD")
	return
}

// askGit runs one hook file against one case and says whether it refused.
// Non-zero is a refusal: that is what git does with a pre-push hook, and a hook
// that crashes therefore reads as refusing — which turns every "allow" row red.
func askGit(hook string, c gitCase, dir, head string) string {
	cmd := exec.Command(hook, c.args...)
	cmd.Dir = dir
	cmd.Env = cleanEnv()
	cmd.Stdin = strings.NewReader(strings.ReplaceAll(c.refs, "%HEAD%", head))
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return "allow"
	case errors.As(err, &ee):
		return "refuse"
	default:
		// ⛔ NEVER "refuse": a hook that could not even START (not executable, a
		// bad interpreter, a path that does not resolve) answers nothing, and
		// calling that a refusal would turn every "refuse" row green for the
		// wrong reason — a probe that cannot tell a guard from a missing file.
		return "unrunnable"
	}
}

// judgeGitHook runs every row for one hook file.
//
// A fixture that cannot be built (no git, a full disk) is a row that reads
// "unbuildable": never a pass, and never a refusal either.
func judgeGitHook(file, hookPath, root string, round int) []gitResult {
	var out []gitResult
	for i, c := range gitCases {
		if c.hook != file {
			continue
		}
		// post-checkout REPAIRS the fixture it runs in, so each row gets its own;
		// and -install judges the same hook twice, so each round gets its own.
		sub := filepath.Join(root, fmt.Sprintf("r%d-%d", round, i))
		dir, head, err := buildFixture(sub, c.fixture)
		got := "unbuildable"
		if err == nil {
			got = askGit(hookPath, c, dir, head)
		}
		out = append(out, gitResult{c, got})
	}
	return out
}

// builtFrom is CONTEXT, never the verdict.
func builtFrom(path string) string {
	if bi, err := buildinfo.ReadFile(path); err == nil && bi != nil {
		return "built from gitsafe " + bi.Main.Version
	}
	return "version unreadable"
}

func resolveHooksDir(flagDir string) (string, error) {
	d := flagDir
	if d == "" {
		g := gitGlobalHooksPath()
		if g == "" {
			return "", errors.New("core.hooksPath is not set globally: git runs no global hook, so none is guarding — `git config --global core.hooksPath ~/.config/git/hooks`")
		}
		d = g
	}
	if d == "~" || strings.HasPrefix(d, "~/") {
		// No home directory leaves a relative path, which names a directory that
		// is not there and is reported as such.
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, strings.TrimPrefix(d, "~"))
	}
	return d, nil
}

// checkGit judges the active git hooks; with install it first recopies the
// ones that answer wrongly, and never touches one that answers right.
func checkGit(install bool, hooksDir, from string, stdout, stderr io.Writer) int {
	dir, err := resolveHooksDir(hooksDir)
	if err != nil {
		fmt.Fprintln(stderr, "hookcheck:", err)
		return 1
	}
	base, err := probeRoot()
	if err != nil {
		fmt.Fprintln(stderr, "hookcheck:", err)
		return 1
	}
	// A MkdirAll that fails leaves MkdirTemp to fail on the same cause.
	_ = os.MkdirAll(base, 0o755)
	root, err := os.MkdirTemp(base, "probe-")
	if err != nil {
		fmt.Fprintln(stderr, "hookcheck:", err)
		return 1
	}
	defer os.RemoveAll(root)

	fmt.Fprintf(stdout, "git hooks directory: %s\n", dir)
	// Absolute, because a probe runs the hook with the fixture as its working
	// directory and a relative path would be resolved against THAT.
	if !filepath.IsAbs(dir) {
		cwd, _ := os.Getwd()
		dir = filepath.Join(cwd, dir)
	}
	code := 0
	for _, h := range gitHookNames {
		path := filepath.Join(dir, h.file)
		var res []gitResult
		_, statErr := os.Stat(path)
		if statErr == nil {
			res = judgeGitHook(h.file, path, root, 0)
		} else {
			// A missing file is git running NOTHING: not a pass.
			fmt.Fprintf(stderr, "hookcheck: %s is not there — git runs no %s hook\n", path, h.file)
		}
		if install && (statErr != nil || anyWrong(res)) {
			if err := recopy(path, h.binary, from, stdout); err != nil {
				fmt.Fprintln(stderr, "hookcheck:", err)
				code = 1
				continue
			}
			res = judgeGitHook(h.file, path, root, 1)
		}
		if res == nil || reportGit(h.file, path, res, stdout, stderr) != 0 {
			code = 1
		}
	}
	return code
}

func anyWrong(res []gitResult) bool {
	for _, r := range res {
		if r.wrong() {
			return true
		}
	}
	return false
}

func reportGit(file, path string, res []gitResult, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "\n%s  (%s; %s)\n", file, path, builtFrom(path))
	fmt.Fprintf(stdout, "%-40s %-8s %s\n", "case", "want", "got")
	wrong := 0
	for _, r := range res {
		mark := ""
		if r.wrong() {
			wrong++
			mark = " ←"
		}
		fmt.Fprintf(stdout, "%-40s %-8s %s%s\n", r.c.name, r.c.want, r.got, mark)
	}
	if wrong > 0 {
		fmt.Fprintf(stderr, "%s is wrong on %d of %d — STALE or broken: `hookcheck -git -install` recopies it\n", path, wrong, len(res))
		return 1
	}
	fmt.Fprintf(stdout, "%s is right on all %d\n", path, len(res))
	return 0
}

// recopy replaces the hook with the current binary, keeping the old file
// beside it. The new file is written whole and renamed in, so git never runs a
// half-written hook.
func recopy(dest, binary, from string, stdout io.Writer) error {
	src := filepath.Join(from, binary)
	if from == "" {
		p, err := exec.LookPath(binary)
		if err != nil {
			return fmt.Errorf("%s is not on PATH to copy from: %w", binary, err)
		}
		src = p
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	old, _ := os.ReadFile(dest) // absent is fine: nothing to keep
	tmp := dest + ".new"
	// In order, stopping at the first failure: the old file is kept BEFORE it is
	// replaced, and nothing replaces it until the new one is fully written.
	for _, step := range []func() error{
		func() error {
			if old == nil {
				return nil
			}
			return os.WriteFile(dest+".bak-"+timeNow().Format("20060102-150405"), old, 0o755)
		},
		func() error { return os.MkdirAll(filepath.Dir(dest), 0o755) },
		func() error { return os.WriteFile(tmp, b, 0o755) },
		func() error { return os.Rename(tmp, dest) },
	} {
		if err := step(); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "recopied %s from %s (%s)\n", dest, src, builtFrom(src))
	return nil
}
