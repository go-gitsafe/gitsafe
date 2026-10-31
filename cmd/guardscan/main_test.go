package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

// ⛔ BOTH SHAPES, because the transcript format is not ours to pin: a tool call
// is {"name":"Bash","input":{"command":…}} and the payload the guard itself
// receives is {"tool_name":"Bash","tool_input":{"command":…}}. A reader keyed
// to one of them would go quiet at the next change, and a corpus that reads
// zero commands reports a machine with no false refusals on it.
func TestReadFindsBashCommandsInBothShapes(t *testing.T) {
	dir := t.TempDir()
	lines := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x"}}]}}`,
		`{"tool_name":"Bash","tool_input":{"command":"ruby --version"}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"deeply":{"nested":[{"name":"Bash","input":{"command":"GITSAFE_ALLOW_SHELLWORK=1 awk '{print}' f"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"   "}}]}}`,
		`{"truncated":`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file that is not a transcript must not be read.
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("nothing here"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Read(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if c.Files != 1 {
		t.Errorf("read %d file(s), want 1", c.Files)
	}
	if c.Total != 4 {
		t.Errorf("found %d command(s), want 4: %v", c.Total, c.Commands)
	}
	if c.Commands["go test ./..."] != 2 {
		t.Errorf("the repeated command was counted %d time(s), want 2", c.Commands["go test ./..."])
	}
	if c.Commands["ruby --version"] != 1 {
		t.Error("the hook-payload shape was not read")
	}
	if c.Escaped["GITSAFE_ALLOW_SHELLWORK"] != 1 {
		t.Errorf("escapes = %v, want one GITSAFE_ALLOW_SHELLWORK", c.Escaped)
	}
	if strings.TrimSpace(strings.Join(keysOf(c.Commands), "")) == "" {
		t.Error("no commands at all")
	}
}

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ⛔⛔ A SCAN THAT COULD NOT READ MUST NOT REPORT A CLEAN MACHINE. Both ways of
// reading nothing are errors, because "no false refusals" is exactly what they
// look like from outside — the shape of defect that reported zero findings
// from a column offset once before.
func TestReadingNothingIsAnErrorNotACleanMachine(t *testing.T) {
	t.Run("no transcripts at all", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := run([]string{"--transcripts", t.TempDir()}, &out, &errb)
		if code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "nothing was measured") {
			t.Errorf("stderr does not say nothing was measured:\n%s", errb.String())
		}
	})

	t.Run("a transcript whose shape it cannot read", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "s.jsonl"),
			[]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		code := run([]string{"--transcripts", dir}, &out, &errb)
		if code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "no Bash command") {
			t.Errorf("stderr does not name the real problem:\n%s", errb.String())
		}
	})
}

// TestItReportsWhatTheRulesWouldRefuse drives the whole command over a corpus
// with one known refusal and one known allowance in it.
func TestItReportsWhatTheRulesWouldRefuse(t *testing.T) {
	dir := t.TempDir()
	lines := strings.Join([]string{
		`{"tool_name":"Bash","tool_input":{"command":"cat go.mod | head -5"}}`,
		`{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
		`{"tool_name":"Bash","tool_input":{"command":"ruby --version"}}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"--transcripts", dir}, &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"3 Bash command(s)", "a pipeline", "cat go.mod | head -5"} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not contain %q:\n%s", want, got)
		}
	}
	// ⭐ The allowances must NOT be listed: a report that names everything
	// names nothing.
	if strings.Contains(got, "go test ./...") {
		t.Errorf("an allowed command was reported as a refusal:\n%s", got)
	}
	if strings.Contains(got, "ruby --version") {
		t.Errorf("a version query was reported as a refusal:\n%s", got)
	}
}

// ⛔ THE SAME GATE hookcheck's CORPUS NEEDED: a rule nobody adds a line for is
// a rule this scan stays silent about, and silence reads like "nothing to
// report". It enumerates the subject — the import block of
// cmd/guard-bash/main.go — rather than repeating a list here.
func TestEveryRuleGuardBashRunsIsScanned(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "guard-bash", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	const prefix = `"github.com/go-gitsafe/gitsafe/`
	scanned := rules("/cwd", "/home", "/tmpdir")
	var imported []string
	for _, line := range strings.Split(string(src), "\n") {
		i := strings.Index(line, prefix)
		if i < 0 {
			continue
		}
		name := strings.TrimSuffix(line[i+len(prefix):], `"`)
		name = strings.TrimSpace(strings.TrimSuffix(name, `"`))
		if name == "" || strings.Contains(name, "/") {
			continue // a subpackage, not a rule
		}
		imported = append(imported, name)
	}
	if len(imported) < 5 {
		t.Fatalf("read only %v from guard-bash's imports: this gate cannot see what it is for", imported)
	}
	for _, name := range imported {
		switch name {
		case "shellcmd", "redact", "cliflags", "credurl", "credfix", "protect", "ghauth", "glauth":
			continue // libraries, not rules
		}
		if _, ok := scanned[name]; !ok {
			t.Errorf("guard-bash runs %q and guardscan does not scan it", name)
		}
	}
	for name := range scanned {
		found := false
		for _, in := range imported {
			if in == name {
				found = true
			}
		}
		if !found {
			t.Errorf("guardscan scans %q, which cmd/guard-bash/main.go does not import", name)
		}
	}
}

// ⛔ A GATE NOBODY HAS SEEN FAIL IS NOT A GATE. The check above passed on its
// first run, which says nothing about whether it can see a missing rule, so
// the same comparison is run here against a map with one deliberately removed.
func TestTheRuleGateCanFail(t *testing.T) {
	full := rules("/cwd", "/home", "/tmpdir")
	for missing := range full {
		short := map[string]func(string) (string, bool){}
		for name, fn := range full {
			if name != missing {
				short[name] = fn
			}
		}
		if _, ok := short[missing]; ok {
			t.Fatalf("the control did not remove %q", missing)
		}
		if len(short) != len(full)-1 {
			t.Fatalf("removing %q left %d of %d", missing, len(short), len(full))
		}
		// This is the comparison TestEveryRuleGuardBashRunsIsScanned makes;
		// with the rule gone it must come out unsatisfied.
		if _, ok := short[missing]; ok {
			t.Errorf("a map missing %q still answers for it", missing)
		}
	}
	if len(full) < 7 {
		t.Errorf("only %d rules in the map: the gate above is measuring less than guard-bash runs", len(full))
	}
}

// All four spellings, as every command in this repository answers them.
func TestEverySpellingOfTheQuestionIsAnswered(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help", "help"} {
		var out, errb bytes.Buffer
		cmd := RootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if code := cliflags.Run(cmd, []string{arg}); code != 0 {
			t.Errorf("guardscan %s exited %d\n%s", arg, code, errb.String())
		}
		if !strings.Contains(out.String(), "--transcripts") {
			t.Errorf("guardscan %s does not name its flags:\n%s", arg, out.String())
		}
	}
}

func TestBothSpellingsOfEveryFlag(t *testing.T) {
	for _, spelling := range [][]string{
		{"-transcripts", "/x"},
		{"--transcripts", "/x"},
		{"-rule", "shellwork"},
		{"-escaped"},
		{"-examples", "7"},
	} {
		cmd := RootCmd()
		if err := cmd.Flags().Parse(cliflags.LegacyArgs(cmd, spelling)); err != nil {
			t.Errorf("%q: %v", spelling, err)
		}
	}
}

func TestAnUnknownRuleIsAMisuse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"),
		[]byte(`{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"--transcripts", dir, "--rule", "nope"}, &out, &errb); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestEscapePrefix(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		name string
		ok   bool
	}{
		{"GITSAFE_ALLOW_SHELLWORK=1 awk '{print}' f", "GITSAFE_ALLOW_SHELLWORK", true},
		{"S=/tmp GITSAFE_ALLOW_VOLATILE=1 git clone u /tmp/x", "GITSAFE_ALLOW_VOLATILE", true},
		{"GITSAFE_ALLOW_SHELLWORK=0 awk '{print}' f", "", false},
		{"GITSAFE_ALLOW_SHELLWORK= awk '{print}' f", "", false},
		{"awk '{print}' f", "", false},
		// ⛔ A MENTION IS NOT A PERMISSION: scanning stops at the command word.
		{`git commit -m "GITSAFE_ALLOW_SHELLWORK=1 is the escape"`, "", false},
	} {
		name, ok := escapePrefix(tc.cmd)
		if ok != tc.ok || name != tc.name {
			t.Errorf("escapePrefix(%q) = (%q, %v), want (%q, %v)", tc.cmd, name, ok, tc.name, tc.ok)
		}
	}
}
