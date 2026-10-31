package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-gitsafe/gitsafe/bypass"
	"github.com/go-gitsafe/gitsafe/discard"
	"github.com/go-gitsafe/gitsafe/pipestatus"
	"github.com/go-gitsafe/gitsafe/secretarg"
	"github.com/go-gitsafe/gitsafe/shellwork"
	"github.com/go-gitsafe/gitsafe/sigpipe"
	"github.com/go-gitsafe/gitsafe/volatile"
)

// fakeHook writes a stub script that answers every payload the same way, so a
// hook's BEHAVIOUR can be arranged without a real guard.
func fakeHook(t *testing.T, name, answer string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	body := "#!/bin/sh\ncat >/dev/null\n"
	switch answer {
	case "refuse":
		body += `printf '{"hookSpecificOutput":{"permissionDecision":"deny"}}'` + "\n"
	case "allow":
		// says nothing, which is how the harness reads an allow
	case "crash":
		body += "exit 3\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func settingsWith(t *testing.T, hooks ...string) string {
	t.Helper()
	var hs []any
	for _, h := range hooks {
		hs = append(hs, map[string]any{"type": "command", "command": h, "timeout": 10})
	}
	doc := map[string]any{
		"permissions": map[string]any{"allow": []any{"Edit(/x/**)"}},
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{"matcher": "Bash", "hooks": hs}},
		},
	}
	p := filepath.Join(t.TempDir(), "settings.json")
	b, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAGuardIsJudgedByWhatItANSWERS.
//
// On 2026-09-29 a guard on this machine refused eight correct commands in one
// session. The first hypothesis was that the installed binary had drifted
// behind its source, and the dates supported it — binary at 10:54, fix the
// same day. That hypothesis was WRONG, and comparing versions could not have
// shown it: the binary was right, and a second hook registered beside it was
// the one refusing.
//
// So this judges each hook by running it. A hook that is behind, one that is a
// leftover, and one that was never correct all look alike from a version
// string and different here.
func TestAGuardIsJudgedByWhatItANSWERS(t *testing.T) {
	// A hook that refuses everything is wrong on every case that must be
	// ALLOWED — which is the half that gets a guard worked around.
	always := fakeHook(t, "always-refuse", "refuse")
	var out, errb bytes.Buffer
	if code := run([]string{"-settings", settingsWith(t, always)}, &out, &errb); code != 1 {
		t.Fatalf("code = %d, want 1\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "wrong on") {
		t.Errorf("stderr: %s", errb.String())
	}
	// And the report names every case, so the reader sees WHICH.
	for _, c := range corpus {
		if !strings.Contains(out.String(), c.name) {
			t.Errorf("the report is missing %q", c.name)
		}
	}

	// A hook that allows everything is wrong on every case that must be
	// refused.
	out.Reset()
	errb.Reset()
	never := fakeHook(t, "always-allow", "allow")
	if code := run([]string{"-settings", settingsWith(t, never)}, &out, &errb); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}

	// A hook that CRASHES reads as "allow", because that is what the harness
	// does with it. Reporting anything else would describe another machine.
	out.Reset()
	errb.Reset()
	crash := fakeHook(t, "crashes", "crash")
	if code := run([]string{"-settings", settingsWith(t, crash)}, &out, &errb); code != 1 {
		t.Fatalf("a crashing hook allows everything, so it must be reported wrong: %d", code)
	}
}

// Two hooks are shown side by side, which is what made the real diagnosis
// possible: they disagreed, and the column said which.
func TestTwoHooksAreComparedColumnByColumn(t *testing.T) {
	a := fakeHook(t, "refuser", "refuse")
	b := fakeHook(t, "allower", "allow")
	var out, errb bytes.Buffer
	run([]string{"-settings", settingsWith(t, a, b)}, &out, &errb)
	head := strings.SplitN(out.String(), "\n", 4)
	joined := strings.Join(head, " ")
	if !strings.Contains(joined, "refuser") || !strings.Contains(joined, "allower") {
		t.Errorf("both hooks must have a column:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "refuser") || !strings.Contains(errb.String(), "allower") {
		t.Errorf("both must be judged:\n%s", errb.String())
	}
}

// NOTHING wired is not a pass. It reads exactly like a clean report unless
// somebody says so, which is the shape that lets an unguarded machine look
// guarded.
func TestNoGuardWiredIsAFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-settings", p}, &out, &errb); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "no Bash guard is wired") {
		t.Errorf("stderr: %s", errb.String())
	}
	// And it says which file it read, so the absence is legible rather than
	// silent — the settings it judges may not be the only ones there are.
	if !strings.Contains(out.String(), p) {
		t.Errorf("the report must name the file it read: %s", out.String())
	}
}

// -install is idempotent, keeps every other key, and backs up what it
// replaces. These settings are not under version control on this machine, so
// the copy is the only way back.
func TestInstallIsIdempotentAndKeepsEverythingElse(t *testing.T) {
	old := timeNow
	timeNow = func() time.Time { return time.Date(2026, 9, 29, 10, 30, 0, 0, time.UTC) }
	t.Cleanup(func() { timeNow = old })

	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	orig := `{"permissions":{"allow":["Edit(/x/**)"]},"effortLevel":"high",` +
		`"hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"date"}]}]}}`
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := wire(p); err != nil {
		t.Fatal(err)
	}
	doc, err := load(p)
	if err != nil {
		t.Fatal(err)
	}
	if doc["effortLevel"] != "high" {
		t.Error("a key this command knows nothing about was lost")
	}
	if _, ok := doc["permissions"]; !ok {
		t.Error("permissions were lost")
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Error("the PostToolUse hooks were lost")
	}
	got, err := bashHooks(p)
	if err != nil || len(got) != 1 || got[0] != wantHook {
		t.Fatalf("hooks = %v, err = %v", got, err)
	}
	if _, err := os.Stat(p + ".bak-20260929-103000"); err != nil {
		t.Errorf("no backup was kept: %v", err)
	}

	// Again: nothing is added twice.
	if err := wire(p); err != nil {
		t.Fatal(err)
	}
	if got, _ := bashHooks(p); len(got) != 1 {
		t.Errorf("wiring twice gave %v", got)
	}
}

// A settings file with no Bash matcher at all, and one that does not exist
// yet: both end with the guard wired and nothing else disturbed.
func TestInstallFromNothing(t *testing.T) {
	old := timeNow
	timeNow = func() time.Time { return time.Unix(0, 0).UTC() }
	t.Cleanup(func() { timeNow = old })

	for _, tc := range []struct{ name, body string }{
		{"no file at all", ""},
		{"no hooks key", `{"effortLevel":"high"}`},
		{"hooks but no Bash matcher", `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[]}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "settings.json")
			if tc.body != "" {
				if err := os.WriteFile(p, []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := wire(p); err != nil {
				t.Fatal(err)
			}
			got, err := bashHooks(p)
			if err != nil || len(got) != 1 || got[0] != wantHook {
				t.Errorf("hooks = %v, err = %v", got, err)
			}
		})
	}
}

// Settings that are not JSON are an error naming the file, not a silent empty
// answer that would read as "no guard wired".
func TestUnreadableSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-settings", p}, &out, &errb); code != 1 {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(errb.String(), p) {
		t.Errorf("the error must name the file: %s", errb.String())
	}
}

// Flags it does not know, and the -install path end to end through run().
func TestRunFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-nope"}, &out, &errb); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}

	old := timeNow
	timeNow = func() time.Time { return time.Unix(0, 0).UTC() }
	t.Cleanup(func() { timeNow = old })
	p := filepath.Join(t.TempDir(), "settings.json")
	out.Reset()
	errb.Reset()
	// Only the SIDE EFFECT is asserted. The exit code depends on whether this
	// machine has a correct guard-bash on PATH — 0 where it does, 1 where it
	// does not — and a test that asserted either would pass in one place and
	// fail in the other, which is worse than failing outright. What the report
	// is worth is covered by the fake hooks above.
	run([]string{"-install", "-settings", p}, &out, &errb)
	if got, _ := bashHooks(p); len(got) != 1 || got[0] != wantHook {
		t.Errorf("hooks = %v", got)
	}
	if !strings.Contains(out.String(), p) {
		t.Errorf("the report must name the file it wired: %s", out.String())
	}
}

// ask reads a hook's answer the way the harness does, and a hook that cannot
// even be run is an allow rather than a crash of this command.
func TestAskOnAHookThatIsNotThere(t *testing.T) {
	if got := ask("/definitely/not/here/guard", "git status"); got != "allow" {
		t.Errorf("got %q", got)
	}
}

// short() names the column after the program, not the whole command line.
func TestShort(t *testing.T) {
	for in, want := range map[string]string{
		"~/.claude/hooks/guard-bash.sh": "guard-bash.sh",
		"guard-bash":                    "guard-bash",
		"/usr/local/bin/g --flag":       "g",
		"":                              "",
	} {
		if got := short(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}

// The corpus has both halves, and neither is allowed to become empty: a
// corpus of refusals alone would pass a guard that refuses everything.
func TestTheCorpusHasBothHalves(t *testing.T) {
	var refuse, allow int
	for _, c := range corpus {
		switch c.want {
		case "refuse":
			refuse++
		case "allow":
			allow++
		default:
			t.Errorf("%q wants %q, which is neither", c.name, c.want)
		}
	}
	if refuse == 0 || allow == 0 {
		t.Fatalf("%d refuse, %d allow — a corpus of one kind proves nothing", refuse, allow)
	}
}

// TestEveryRuleGuardBashRunsFiresOnThisCorpus is the gate that was missing.
//
// ⛔ MEASURED 2026-10-31: `grep -c volatile cmd/hookcheck/main.go` printed 0.
// The `volatile` rule had shipped in v0.20.0 — written after FIVE recorded
// losses of work, the last a whole branch purged with the scratchpad before its
// first push — and the table that prints "right on all 32" had never asked it
// anything. The number was a verdict about a smaller table than the guard, and
// nothing said so: a corpus going quiet about a rule looks exactly like a rule
// with nothing to say.
//
// ⛔ IT ENUMERATES THE RULES FROM guard-bash's OWN SOURCE rather than from a
// list written here. A list has to be remembered; an import block cannot be
// forgotten, because adding a rule to the guard without a row here then fails
// this test. The adapters below are the one hand-written part, and a rule with
// no adapter is an explicit failure rather than a silent pass.
func TestEveryRuleGuardBashRunsFiresOnThisCorpus(t *testing.T) {
	// The directory and $TMPDIR the cwd-dependent rule is asked about. Fixed,
	// so the answer does not depend on where the test runs.
	const (
		cwd    = "/Users/someone/Documents/VCS/GIT/localhost/go-gitsafe-gitsafe"
		home   = "/Users/someone"
		tmpdir = "/var/folders/zz/t"
	)
	fires := map[string]func(string) bool{
		"bypass":     func(c string) bool { return bypass.Check(c).Found() },
		"secretarg":  func(c string) bool { return secretarg.Check(c).Found() },
		"discard":    func(c string) bool { return discard.Check(c).Found() },
		"pipestatus": func(c string) bool { return pipestatus.Check(c).Found() },
		"sigpipe":    func(c string) bool { return sigpipe.Check(c).Found() },
		"shellwork":  func(c string) bool { return shellwork.Check(c).Found() },
		"volatile":   func(c string) bool { return volatile.Check(c, cwd, home, tmpdir).Found() },
	}

	src, err := os.ReadFile(filepath.Join("..", "guard-bash", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	const prefix = `"github.com/go-gitsafe/gitsafe/`
	var rules []string
	for rest := string(src); ; {
		i := strings.Index(rest, prefix)
		if i < 0 {
			break
		}
		rest = rest[i+len(prefix):]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			break
		}
		if name := rest[:j]; !imported(rules, name) {
			rules = append(rules, name)
		}
	}
	if len(rules) == 0 {
		t.Fatal("read no gitsafe imports out of cmd/guard-bash/main.go: the parse is wrong, not the guard")
	}

	// ⛔ THE PLANTED POSITIVE FIRST: over an EMPTY corpus every rule must come
	// back unasked. Without this half, a predicate that could never report
	// absence — a mistyped package name, a Found() read the wrong way round —
	// would pass this test for the same reason a complete corpus does, and the
	// gap it exists to catch would stay invisible.
	if silent := unasked(nil, rules, fires); len(silent) != len(fires) {
		t.Fatalf("over an empty corpus only %v read as unasked, of %d rules: "+
			"this measurement cannot see the absence it is for", silent, len(fires))
	}

	var cmds []string
	for _, c := range corpus {
		cmds = append(cmds, c.cmd)
	}
	if u := unasked(cmds, rules, fires); len(u) > 0 {
		t.Errorf("guard-bash runs %v, and no case in this corpus makes %v answer: "+
			"a rule the table never asks about is one 'right on all %d' says nothing about",
			rules, u, len(corpus))
	}
	// And the other direction: an adapter for a rule guard-bash does NOT run
	// would quietly demand rows for a rule nobody is protected by.
	for name := range fires {
		if !imported(rules, name) {
			t.Errorf("this test asks %q, which cmd/guard-bash/main.go does not import", name)
		}
	}
}

// unasked names the rules that no command in cmds makes answer. A rule
// guard-bash imports but this test has no adapter for is skipped here and
// reported by the caller, which is the difference between "we do not ask it"
// and "nothing asks it".
func unasked(cmds []string, rules []string, fires map[string]func(string) bool) []string {
	var out []string
	for _, r := range rules {
		ask, ok := fires[r]
		if !ok {
			continue // cliflags and the other libraries are not rules
		}
		answered := false
		for _, c := range cmds {
			if ask(c) {
				answered = true
				break
			}
		}
		if !answered {
			out = append(out, r)
		}
	}
	return out
}

func imported(rules []string, name string) bool {
	for _, r := range rules {
		if r == name {
			return true
		}
	}
	return false
}

// With no -settings, it reads ~/.claude/settings.json — the path anyone
// actually runs it on.
func TestTheDefaultSettingsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	timeNow = func() time.Time { return time.Unix(0, 0).UTC() }
	t.Cleanup(func() { timeNow = old })

	var out, errb bytes.Buffer
	// Nothing there yet: not a pass.
	if code := run(nil, &out, &errb); code != 1 {
		t.Errorf("an absent settings file must not read as a clean report: %d", code)
	}
	if !strings.Contains(out.String(), filepath.Join(home, ".claude", "settings.json")) {
		t.Errorf("the report must name the path it chose: %s", out.String())
	}

	// And -install creates it, directory and all.
	out.Reset()
	errb.Reset()
	run([]string{"-install"}, &out, &errb)
	got, err := bashHooks(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || len(got) != 1 || got[0] != wantHook {
		t.Errorf("hooks = %v, err = %v", got, err)
	}
}

// A settings file that cannot be written is an error, not a silent no-op that
// would leave the machine unguarded while reporting success.
func TestInstallWhenTheFileCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot make a read-only directory here")
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := wire(filepath.Join(dir, "settings.json")); err == nil {
		t.Error("writing into a read-only directory must be an error")
	}
}
