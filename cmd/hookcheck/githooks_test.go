package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realGuards builds the CURRENT guards once per test into a directory laid out
// as a hooks directory (pre-push, post-checkout) and as a source directory
// (git-pre-push-guard, git-post-checkout-guard).
func realGuards(t *testing.T) (hooks, src string) {
	t.Helper()
	hooks, src = t.TempDir(), t.TempDir()
	for _, g := range []struct{ pkg, hook, bin string }{
		{"git-pre-push-guard", "pre-push", "git-pre-push-guard"},
		{"git-post-checkout-guard", "post-checkout", "git-post-checkout-guard"},
	} {
		for _, dst := range []string{filepath.Join(hooks, g.hook), filepath.Join(src, g.bin)} {
			out, err := exec.Command("go", "build", "-o", dst, "github.com/go-gitsafe/gitsafe/cmd/"+g.pkg).CombinedOutput()
			if err != nil {
				t.Fatalf("building %s: %v\n%s", g.pkg, err, out)
			}
		}
	}
	return hooks, src
}

// scriptHooks makes a hooks directory whose two files run one shell body.
func scriptHooks(t *testing.T, body string) string {
	t.Helper()
	d := t.TempDir()
	for _, n := range []string{"pre-push", "post-checkout"} {
		if err := os.WriteFile(filepath.Join(d, n), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func aimProbes(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	// The Go caches are pinned BEFORE HOME moves, so building the guards keeps
	// using them.
	for _, name := range []string{"GOCACHE", "GOMODCACHE"} {
		out, err := exec.Command("go", "env", name).Output()
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(name, strings.TrimSpace(string(out)))
	}
	// HOME is a regular FILE: the Bash-guard path, which looks for
	// ~/.claude/settings.json, then fails at once instead of reading the real
	// one or wiring a new one and running its whole corpus.
	home := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(home, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	old := probeRoot
	probeRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { probeRoot = old })
}

// The control. A probe that cannot tell a current guard from a wrong one is the
// defect this check exists to prevent, so the current guards must pass and each
// kind of wrong hook must fail — on the rows that tell it apart.
func TestGitHooksAreJudgedByWhatTheyAnswer(t *testing.T) {
	aimProbes(t)
	hooks, src := realGuards(t)

	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-hooks-dir", hooks, "-from", src}, &out, &errb); code != 0 {
		t.Fatalf("the current guards must be right, got %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "built from gitsafe") {
		t.Errorf("the version is printed as context:\n%s", out.String())
	}

	for _, tc := range []struct{ name, body, wantRow string }{
		// A guard from before a rule existed ALLOWS what the rule refuses.
		{"allows everything", "cat >/dev/null; exit 0", "a 1 MiB blob in a commit pushed"},
		// A guard that refuses everything is wrong on the rows that must pass.
		{"refuses everything", "cat >/dev/null; exit 1", "an ordinary branch, small files"},
	} {
		out.Reset()
		errb.Reset()
		if code := run([]string{"-git", "-hooks-dir", scriptHooks(t, tc.body)}, &out, &errb); code != 1 {
			t.Errorf("%s: code = %d, want 1", tc.name, code)
		}
		if !strings.Contains(errb.String(), "STALE or broken") {
			t.Errorf("%s: stderr: %s", tc.name, errb.String())
		}
		if !strings.Contains(out.String(), tc.wantRow) {
			t.Errorf("%s: report lacks %q", tc.name, tc.wantRow)
		}
	}
}

// A hook that cannot START is not a refusal. Calling it one would turn every
// "refuse" row green for a file that is not a guard at all.
func TestAHookThatCannotStartIsNotARefusal(t *testing.T) {
	aimProbes(t)
	d := t.TempDir()
	for _, n := range []string{"pre-push", "post-checkout"} {
		if err := os.WriteFile(filepath.Join(d, n), []byte("not executable"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-hooks-dir", d}, &out, &errb); code != 1 {
		t.Fatalf("code = %d, want 1\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "unrunnable") {
		t.Errorf("the report must say it could not run:\n%s", out.String())
	}
	if strings.Count(out.String(), "unrunnable") < len(gitCases) {
		t.Errorf("every row must read unrunnable:\n%s", out.String())
	}
}

// The probe runs the file NAMED pre-push, wherever the directory is given,
// including as a relative path (the fixture is the working directory).
func TestARelativeHooksDirIsResolvedBeforeTheFixtureBecomesTheWorkingDirectory(t *testing.T) {
	aimProbes(t)
	hooks, src := realGuards(t)
	t.Chdir(filepath.Dir(hooks))
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-hooks-dir", filepath.Base(hooks), "-from", src}, &out, &errb); code != 0 {
		t.Fatalf("code = %d\n%s%s", code, out.String(), errb.String())
	}
}

// -install recopies what answers wrongly, keeps the old file beside it, and
// leaves a hook that answers right exactly as it was.
func TestInstallRecopiesOnlyWhatIsStale(t *testing.T) {
	aimProbes(t)
	good, src := realGuards(t)
	stale := scriptHooks(t, "cat >/dev/null; exit 0")

	// Make post-checkout the current one, pre-push the stale one.
	cur, err := os.ReadFile(filepath.Join(good, "post-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	pc := filepath.Join(stale, "post-checkout")
	if err := os.WriteFile(pc, cur, 0o755); err != nil {
		t.Fatal(err)
	}
	old := timeNow
	timeNow = func() time.Time { return time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { timeNow = old })

	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-install", "-hooks-dir", stale, "-from", src}, &out, &errb); code != 0 {
		t.Fatalf("code = %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "recopied "+filepath.Join(stale, "pre-push")) {
		t.Errorf("pre-push must have been recopied:\n%s", out.String())
	}
	if strings.Contains(out.String(), "recopied "+pc) {
		t.Errorf("a hook that answers right must not be touched:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(stale, "pre-push.bak-20261122-200000")); err != nil {
		t.Errorf("the old file is kept beside it: %v", err)
	}
	if _, err := os.Stat(pc + ".bak-20261122-200000"); err == nil {
		t.Error("an untouched hook got a backup")
	}
	if after, _ := os.ReadFile(pc); !bytes.Equal(after, cur) {
		t.Error("the current hook changed")
	}

	// And it is idempotent: nothing is stale now, so nothing is recopied.
	out.Reset()
	errb.Reset()
	if code := run([]string{"-git", "-install", "-hooks-dir", stale, "-from", src}, &out, &errb); code != 0 {
		t.Fatalf("second run: code = %d\n%s%s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "recopied") {
		t.Errorf("second run recopied:\n%s", out.String())
	}
}

// Without -install a stale hook is only reported, never rewritten.
func TestWithoutInstallNothingIsWritten(t *testing.T) {
	aimProbes(t)
	stale := scriptHooks(t, "cat >/dev/null; exit 0")
	before, _ := os.ReadFile(filepath.Join(stale, "pre-push"))
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-hooks-dir", stale}, &out, &errb); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if after, _ := os.ReadFile(filepath.Join(stale, "pre-push")); !bytes.Equal(before, after) {
		t.Error("the hook was rewritten without -install")
	}
	ents, _ := os.ReadDir(stale)
	if len(ents) != 2 {
		t.Errorf("files appeared beside the hooks: %v", ents)
	}
}

// If the copy source is itself stale, -install says so and fails: it does not
// report a recopy as a cure.
func TestInstallFromAStaleSourceStillFails(t *testing.T) {
	aimProbes(t)
	stale := scriptHooks(t, "cat >/dev/null; exit 0")
	src := t.TempDir()
	for _, n := range []string{"git-pre-push-guard", "git-post-checkout-guard"} {
		if err := os.WriteFile(filepath.Join(src, n), []byte("#!/bin/sh\ncat >/dev/null; exit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-install", "-hooks-dir", stale, "-from", src}, &out, &errb); code != 1 {
		t.Fatalf("code = %d, want 1\n%s", code, out.String())
	}
}

// A hooks directory with no such file is git running nothing: not a pass.
func TestAMissingHookIsAFailureAndInstallCreatesIt(t *testing.T) {
	aimProbes(t)
	d := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-hooks-dir", d}, &out, &errb); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "is not there") {
		t.Errorf("stderr: %s", errb.String())
	}
	_, src := realGuards(t)
	out.Reset()
	errb.Reset()
	if code := run([]string{"-git", "-install", "-hooks-dir", d, "-from", src}, &out, &errb); code != 0 {
		t.Fatalf("install: code = %d\n%s%s", code, out.String(), errb.String())
	}
}

func TestGitWithoutAHooksPathIsAFailure(t *testing.T) {
	aimProbes(t)
	old := gitGlobalHooksPath
	gitGlobalHooksPath = func() string { return "" }
	t.Cleanup(func() { gitGlobalHooksPath = old })
	var out, errb bytes.Buffer
	if code := run([]string{"-git"}, &out, &errb); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "core.hooksPath is not set") {
		t.Errorf("stderr: %s", errb.String())
	}
}

// oldCopies makes a hooks directory whose two hooks answer exactly as the real
// guards (they exec them) but are a different FILE, marked as an older build.
func oldCopies(t *testing.T, src string) string {
	t.Helper()
	d := t.TempDir()
	for _, h := range gitHookNames {
		body := "#!/bin/sh\n# OLDBUILD\nexec " + filepath.Join(src, h.binary) + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(d, h.file), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// versionsByMark stands the marked copies in as v0.45.0 builds and everything
// else as v0.59.0, as on 2026-11-22.
func versionsByMark(t *testing.T) {
	t.Helper()
	old := buildVersion
	buildVersion = func(path string) string {
		b, _ := os.ReadFile(path)
		if bytes.Contains(b, []byte("OLDBUILD")) {
			return "v0.45.0"
		}
		return "v0.59.0"
	}
	t.Cleanup(func() { buildVersion = old })
}

// THE INCIDENT. The active hooks were v0.45.0 beside v0.59.0 binaries, and
// every row passes for both because the guards' rules did not change. It must
// not read as a pass: exit 3, the statement says why, the rows stay printed.
func TestAnOlderBuildThatAnswersTheSameIsUndeterminedNotGreen(t *testing.T) {
	aimProbes(t)
	_, src := realGuards(t)
	hooks := oldCopies(t, src)
	versionsByMark(t)
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-hooks-dir", hooks, "-from", src}, &out, &errb); code != exitUndetermined {
		t.Fatalf("code = %d, want %d\n%s%s", code, exitUndetermined, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "is right on all 4") {
		t.Errorf("the rows are still reported:\n%s", out.String())
	}
	for _, want := range []string{"UNDETERMINED", "v0.45.0", "v0.59.0", "not a pass", "staletools -releases"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errb.String())
		}
	}
	if m, _ := filepath.Glob(filepath.Join(hooks, "*.bak-*")); len(m) != 0 {
		t.Errorf("without -install nothing is written: %v", m)
	}
}

// -install recopies what is undetermined too, and then it is decided.
func TestInstallRecopiesAnUndeterminedBuildAndThenItIsGreen(t *testing.T) {
	aimProbes(t)
	_, src := realGuards(t)
	hooks := oldCopies(t, src)
	versionsByMark(t)
	old := timeNow
	timeNow = func() time.Time { return time.Date(2026, 11, 22, 21, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { timeNow = old })
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-install", "-hooks-dir", hooks, "-from", src}, &out, &errb); code != 0 {
		t.Fatalf("code = %d\n%s%s", code, out.String(), errb.String())
	}
	if strings.Count(out.String(), "recopied") != 2 {
		t.Errorf("both hooks are recopied:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(hooks, "pre-push.bak-20261122-210000")); err != nil {
		t.Errorf("the old copy is kept: %v", err)
	}
	if strings.Contains(errb.String(), "UNDETERMINED") {
		t.Errorf("after the recopy it is the source's build:\n%s", errb.String())
	}
}

// A file with no build information cannot be vouched for either, and neither
// can a missing source: both are undetermined, never green.
func TestAnUnreadableVersionIsUndetermined(t *testing.T) {
	aimProbes(t)
	_, src := realGuards(t)
	hooks := oldCopies(t, src)
	for name, from := range map[string]string{"script hook": src, "source missing": t.TempDir()} {
		var out, errb bytes.Buffer
		if code := run([]string{"-git", "-hooks-dir", hooks, "-from", from}, &out, &errb); code != exitUndetermined {
			t.Errorf("%s: code = %d, want %d\n%s%s", name, code, exitUndetermined, out.String(), errb.String())
		}
		if !strings.Contains(errb.String(), "version unreadable") {
			t.Errorf("%s: the unreadable version is named:\n%s", name, errb.String())
		}
	}
}

func TestGitCorpusHasBothHalvesForEachHook(t *testing.T) {
	seen := map[string]map[string]int{}
	for _, c := range gitCases {
		if seen[c.hook] == nil {
			seen[c.hook] = map[string]int{}
		}
		seen[c.hook][c.want]++
	}
	for _, h := range gitHookNames {
		if seen[h.file]["refuse"] == 0 || seen[h.file]["allow"] == 0 {
			t.Errorf("%s needs at least one refuse row and one allow row: %v", h.file, seen[h.file])
		}
	}
}

// Without git on PATH no fixture can be built: every row says so and the exit
// is 1 — never a pass. Under -install the source cannot be found either.
func TestFixturesThatCannotBeBuiltAreNotAPass(t *testing.T) {
	aimProbes(t)
	hooks := scriptHooks(t, "cat >/dev/null; exit 1")
	t.Setenv("PATH", "")
	for _, tc := range []struct {
		args []string
		want string
		in   func(out, errb string) string
	}{
		{[]string{"-git", "-hooks-dir", hooks}, "unbuildable", func(out, _ string) string { return out }},
		{[]string{"-git", "-install", "-hooks-dir", hooks}, "not on PATH", func(_, errb string) string { return errb }},
	} {
		var out, errb bytes.Buffer
		if code := run(tc.args, &out, &errb); code != 1 {
			t.Errorf("%v: code = %d, want 1\n%s%s", tc.args, code, out.String(), errb.String())
		}
		if !strings.Contains(tc.in(out.String(), errb.String()), tc.want) {
			t.Errorf("%v: want %q:\n%s%s", tc.args, tc.want, out.String(), errb.String())
		}
	}
}

func TestAScriptHookHasNoReadableVersion(t *testing.T) {
	aimProbes(t)
	var out, errb bytes.Buffer
	run([]string{"-git", "-hooks-dir", scriptHooks(t, "cat >/dev/null; exit 0")}, &out, &errb)
	if !strings.Contains(out.String(), "version unreadable") {
		t.Errorf("a script has no build info, and the report says so:\n%s", out.String())
	}
}

// The hooks are RIGHT, so the only reason to fail is the probe root, and
// nothing is reported as judged.
func TestAProbeRootThatCannotBeUsedFails(t *testing.T) {
	aimProbes(t)
	hooks, _ := realGuards(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]func() (string, error){
		"no cache dir":         func() (string, error) { return "", os.ErrPermission },
		"under a regular file": func() (string, error) { return filepath.Join(file, "under"), nil },
	} {
		probeRoot = root
		var out, errb bytes.Buffer
		if code := run([]string{"-git", "-hooks-dir", hooks}, &out, &errb); code != 1 {
			t.Errorf("%s: code = %d, want 1\n%s%s", name, code, out.String(), errb.String())
		}
		if strings.Contains(out.String(), "git hooks directory") || !strings.HasPrefix(errb.String(), "hookcheck: ") {
			t.Errorf("%s: nothing may be judged, the reason must be said:\n%s%s", name, out.String(), errb.String())
		}
	}
}

func TestTheDefaultProbeRootNeedsACacheDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LocalAppData", "")
	if d, err := probeRoot(); err == nil {
		t.Errorf("no cache directory is known, got %q", d)
	}
	t.Setenv("HOME", t.TempDir())
	d, err := probeRoot()
	if err != nil || filepath.Base(d) != "gitsafe-hookcheck" {
		t.Errorf("probeRoot = %q, %v", d, err)
	}
}

func TestInstallFromAnEmptySourceFails(t *testing.T) {
	aimProbes(t)
	hooks := scriptHooks(t, "cat >/dev/null; exit 0")
	before, err := os.ReadFile(filepath.Join(hooks, "pre-push"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-install", "-hooks-dir", hooks, "-from", t.TempDir()}, &out, &errb); code != 1 {
		t.Errorf("code = %d, want 1\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "no such file") {
		t.Errorf("the reason must be said:\n%s", errb.String())
	}
	after, _ := os.ReadFile(filepath.Join(hooks, "pre-push"))
	if !bytes.Equal(before, after) || strings.Contains(out.String(), "recopied") {
		t.Errorf("an unreadable source must leave the hook as it was:\n%s", out.String())
	}
	if m, _ := filepath.Glob(filepath.Join(hooks, "*.bak-*")); len(m) != 0 {
		t.Errorf("nothing was replaced, so nothing is kept: %v", m)
	}
}

func TestInstallIntoAReadOnlyDirectoryFails(t *testing.T) {
	aimProbes(t)
	hooks := scriptHooks(t, "cat >/dev/null; exit 0")
	_, src := realGuards(t)
	if err := os.Chmod(hooks, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(hooks, 0o755) })
	var out, errb bytes.Buffer
	if code := run([]string{"-git", "-install", "-hooks-dir", hooks, "-from", src}, &out, &errb); code != 1 {
		t.Errorf("code = %d, want 1\n%s%s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "recopied") || !strings.Contains(errb.String(), "permission denied") {
		t.Errorf("a failed write is reported and never announced as done:\n%s%s", out.String(), errb.String())
	}
}
