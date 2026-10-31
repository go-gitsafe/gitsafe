// Command hookcheck says what guards are wired into this machine's harness,
// and whether they actually behave.
//
//	hookcheck            # report
//	hookcheck -install   # wire guard-bash in, backing the settings up first
//
// # Why by BEHAVIOUR and not by version
//
// On 2026-09-29 a guard on this machine refused eight correct commands in one
// session — a quoted heredoc that merely QUOTED a forbidden form, a commit
// whose message mentioned one, a subcommand asked for its help. The first
// hypothesis was that the installed binary had drifted behind its source, and
// the dates supported it: the binary was from 10:54 and the fix from the same
// day.
//
// That hypothesis was wrong, and comparing dates could not have shown it. What
// showed it was running both and comparing verdicts: the binary was right, and
// a SECOND hook — a shell script registered beside it — was the one refusing.
//
// So this does not read a version. It finds every PreToolUse hook the harness
// will run for Bash, feeds each one the same payloads, and prints what each
// answered. A hook that is behind, a hook that is a leftover, and a hook that
// was never correct all look the same from a version string and different
// here.
//
// # What it cannot do
//
// It judges the hooks the SETTINGS name. A hook wired somewhere else — a
// project-level settings file, an enterprise policy — is not in the answer,
// and the report says which file it read so that absence is legible rather
// than silent.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/cliflags"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// The corpus. Each case is a command and what a correct guard owes it.
//
// The false-ALLOW half and the false-REFUSE half are both here on purpose. A
// guard that lets a secret through is the danger; a guard that refuses correct
// commands is the one people learn to work around, and then it protects
// nothing at all.
var corpus = []struct {
	name string
	cmd  string
	want string // "refuse" or "allow"
}{
	{"classic token in argv", "./x.sh --token " + ghp, "refuse"},
	{"GitLab token in argv", "./x.sh --token " + glpat, "refuse"},
	{"no-prefix token in argv", "./x.sh --token " + bare, "refuse"},
	{"substitution into argv", `curl -H "Bearer $(cat ~/.github-token)" https://x`, "refuse"},
	{"unquoted heredoc", "cat <<EOF\n--token " + ghp + "\nEOF", "refuse"},
	{"plain push", gitPush, "refuse"},
	{"hand merge", ghMerge, "refuse"},
	{"identity on the line", "git -c user.email=me@example.com commit -m x", "refuse"},
	{"discarding the work tree", "git checkout -- .", "refuse"},
	// ⛔ Written three times in one afternoon on 2026-10-11, each time printing
	// status=0 over a failed build. hookcheck has to know the rule exists, or
	// its "right on all N" is a verdict on a smaller table than the guard.
	{"a status after a pipeline", `go build ./... | tail -5; echo "status=$?"`, "refuse"},
	// ⛔ The SIBLING damage: run on 2026-09-06 against a third party's
	// repository and written up as a defect in THEIR module. Re-run without the
	// pipe it exited 0. `head` had killed `tidy` five lines in.
	{"a producer killed by its consumer", `timeout 400 go mod tidy 2>&1 | head -5`, "refuse"},
	// ⛔ The spelling that went through the FIRST version of that rule, found by
	// running the real command in the real harness rather than only in a table:
	// `command head` reads as the program `command`.
	{"the consumer behind a wrapper", `timeout 400 go mod tidy 2>&1 | command head -5`, "refuse"},

	{"writing about it, quoted", "cat <<'EOF'\n" + gitPush + "\nEOF", "allow"},
	{"a message that mentions it", "git commit -m 'about " + gitPush + "'", "allow"},
	{"a token FILE, not its value", "./x.sh --token-file ~/.t", "allow"},
	{"the wrapper itself", "gitpush origin main", "allow"},
	{"a subcommand that shares the word", "git stash push -m wip", "allow"},
	{"asking for help", ghMerge + " --help", "allow"},
	// ⛔ THESE FIVE USED TO BE "allow", and each proved that pipestatus or
	// sigpipe does not over-refuse. They are refused now — by shellwork, for a
	// different reason: the instruction file's second section says Bash invokes
	// tools and does not pipe. Nothing is lost by flipping them here, because
	// the discrimination they were written for lives in pipestatus_test.go and
	// sigpipe_test.go, in exactly these forms. What this corpus tests is the
	// WIRING, and a case whose verdict now comes from a third rule still tests
	// that.
	{"pipefail does not excuse a pipeline from the Go rule", `set -o pipefail; go build ./... | tail -5; echo "status=$?"`, "refuse"},
	{"tail reads to the end, and is still a pipeline", `go build ./... | tail -5`, "refuse"},
	{"a file read again for free, still a pipeline", `cat go.mod | head -5`, "refuse"},
	{"a read-only producer, still a pipeline", `go list ./... | head -5`, "refuse"},
	{"a wrapped consumer, still a pipeline", `cat go.mod | command head -5`, "refuse"},
	// And the three shapes of shell work the file names, which the operator
	// typed on 2026-10-18 after reading the rule three times.
	{"awk doing the processing", `go version -m ~/.local/bin/gitpush | awk '/build/ {print $3}'`, "refuse"},
	{"a shell loop", `for i in 1 2 3; do echo "$i"; done`, "refuse"},
	{"a commit body on stdin rather than in a file", "git commit -F -", "refuse"},

	// ⛔ THE NEWEST RULE WAS THE ONE THIS TABLE NEVER ASKED ABOUT. `volatile`
	// shipped in v0.20.0, after five recorded losses of work written where a
	// reboot empties it — the last a whole branch, purged with the scratchpad
	// before its first push — and on 2026-10-31 `grep -c volatile
	// cmd/hookcheck/main.go` printed **0**. So "right on all 32" was a verdict
	// about a smaller table than the guard, exactly what the comment above the
	// pipeline rows says about this corpus, and the rule with the freshest
	// scar was outside it.
	//
	// Every row here is destination-explicit, so the answer does not depend on
	// where hookcheck itself is run: `git -C` and `cd` carry the directory, and
	// the paths are absolute or `~`-anchored.
	{"a clone into /tmp", "git clone https://github.com/go-gitsafe/gitsafe /tmp/gitsafe", "refuse"},
	{"a clone into the session scratchpad", "git clone https://github.com/go-gitsafe/gitsafe /private/tmp/claude-501/scratch/gitsafe", "refuse"},
	{"gh repo clone into the $TMPDIR tree", "gh repo clone go-gitsafe/gitsafe /var/folders/zz/t/gitsafe", "refuse"},
	{"a worktree where a reboot empties", "git worktree add /tmp/gitsafe-wt main", "refuse"},
	{"a repository started there", "git init /dev/shm/scratch", "refuse"},
	{"a commit in such a tree, named by -C", "git -C /private/tmp/scratch/gitsafe commit -F /tmp/msg.txt", "refuse"},
	{"cd first, commit second", "cd /tmp/scratch && git commit -m wip", "refuse"},

	{"the same clone, somewhere durable", "git clone https://github.com/go-gitsafe/gitsafe ~/Documents/VCS/GIT/localhost/go-gitsafe-gitsafe-probe", "allow"},
	{"a worktree beside it, durable", "git worktree add ~/Documents/VCS/GIT/localhost/gitsafe-wt main", "allow"},
	// ⭐ /var/tmp is deliberately NOT in the list: it survives a reboot. A row
	// for it is how that decision stays a decision rather than an oversight.
	{"/var/tmp survives a reboot", "git clone https://github.com/go-gitsafe/gitsafe /var/tmp/gitsafe", "allow"},
	{"the escape, which is an act", "GITSAFE_ALLOW_VOLATILE=1 git clone https://github.com/go-gitsafe/gitsafe /tmp/throwaway", "allow"},
	{"a status in a tree already there", "git -C /tmp/gitsafe status --short", "allow"},

	{"a status with no pipeline before it", "go build ./...; echo $?", "allow"},
	{"a redirect is not a pipe, and is what the rule asks for", `go test ./... > /tmp/out.txt 2>&1`, "allow"},
	{"a commit body in a FILE", `git commit -F /tmp/msg.txt`, "allow"},
	{"go run, the sanctioned way to process", `go run ./internal/macapp -install`, "allow"},
	{"an interpreter named as an argument is not running", `grep -rn sed --include="*.go" .`, "allow"},
	// ⭐ The three that keep the sibling rule from refusing ordinary work:
	// `tail` reads to the end so the build finishes, a file can be read again
	// for free, and a read-only producer abandons nothing.
	{"harmless", "go test ./...", "allow"},
}

// Split so that writing this file is not itself a forbidden command — the
// guard reads the text of what runs, and a literal here would be one.
var (
	ghp      = "gh" + "p_0123456789abcdefghijklmnopqrstuvwxyz"
	glpat    = "glpat-" + "0123456789abcdefghij"
	bare     = "AABF3JGZDX3P5PMEXLND6TS6FCWO6"
	gitPush  = "git" + " push origin main"
	ghMerge  = "gh" + " pr merge 42"
	wantHook = "guard-bash"
)

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hookcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	install := fs.Bool("install", false, "wire "+wantHook+" into the settings, backing them up first, then report")
	settings := fs.String("settings", "", "the settings file to read (default ~/.claude/settings.json)")
	if code, ok := cliflags.ParseFlags(fs, args); !ok {
		return code
	}
	path := *settings
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, "hookcheck:", err)
			return 1
		}
		path = filepath.Join(home, ".claude", "settings.json")
	}

	if *install {
		if err := wire(path); err != nil {
			fmt.Fprintln(stderr, "hookcheck:", err)
			return 1
		}
	}

	hooks, err := bashHooks(path)
	if err != nil {
		fmt.Fprintln(stderr, "hookcheck:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s names %d PreToolUse hook(s) for Bash\n", path, len(hooks))
	// Nothing wired is not a pass. It is the state this exists to make
	// visible, and it reads exactly like a clean report if nobody says so.
	if len(hooks) == 0 {
		fmt.Fprintf(stderr, "hookcheck: no Bash guard is wired — `hookcheck -install` writes one\n")
		return 1
	}
	return report(hooks, stdout, stderr)
}

func report(hooks []string, stdout, stderr io.Writer) int {
	wrong := map[string]int{}
	fmt.Fprintf(stdout, "\n%-32s %-8s", "case", "want")
	for _, h := range hooks {
		fmt.Fprintf(stdout, " %-14s", short(h))
	}
	fmt.Fprintln(stdout)
	for _, c := range corpus {
		fmt.Fprintf(stdout, "%-32s %-8s", c.name, c.want)
		for _, h := range hooks {
			got := ask(h, c.cmd)
			mark := ""
			if got != c.want {
				wrong[h]++
				mark = " ←"
			}
			fmt.Fprintf(stdout, " %-14s", got+mark)
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintln(stdout)
	code := 0
	for _, h := range hooks {
		if n := wrong[h]; n > 0 {
			fmt.Fprintf(stderr, "%s is wrong on %d of %d\n", h, n, len(corpus))
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "%s is right on all %d\n", h, len(corpus))
	}
	return code
}

// ask runs one hook with one payload and reads its answer.
//
// A hook that says nothing is allowing: that is the harness's own rule, and a
// hook which crashes therefore reads as "allow" here exactly as it would in
// production. Reporting it as anything else would describe a machine other
// than this one.
func ask(hook, cmd string) string {
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]string{"command": cmd},
	})
	if err != nil {
		return "?"
	}
	c := execCommand("/bin/sh", "-c", hook)
	c.Stdin = strings.NewReader(string(payload))
	out, _ := c.Output()
	if strings.Contains(strings.ToLower(string(out)), `"deny"`) {
		return "refuse"
	}
	return "allow"
}

var execCommand = exec.Command

func short(hook string) string {
	f := strings.Fields(hook)
	if len(f) == 0 {
		return hook
	}
	return filepath.Base(f[0])
}

// settingsDoc is read and written as a generic document, so every key this
// command does not know about survives the round trip.
type settingsDoc map[string]any

func bashHooks(path string) ([]string, error) {
	doc, err := load(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matchers(doc) {
		if s, _ := m["matcher"].(string); s != "Bash" {
			continue
		}
		hs, _ := m["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); cmd != "" {
				out = append(out, cmd)
			}
		}
	}
	return out, nil
}

func matchers(doc settingsDoc) []map[string]any {
	hooks, _ := doc["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	var out []map[string]any
	for _, m := range pre {
		if mm, ok := m.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}

func load(path string) (settingsDoc, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settingsDoc{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc settingsDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// wire adds the guard to the settings if it is not already there, keeping
// every other key. It is idempotent, and it never removes a hook it does not
// recognise: a guard somebody else added is not this command's to judge.
func wire(path string) error {
	doc, err := load(path)
	if err != nil {
		return err
	}
	for _, m := range matchers(doc) {
		if s, _ := m["matcher"].(string); s != "Bash" {
			continue
		}
		hs, _ := m["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); short(cmd) == wantHook {
				return nil // already wired
			}
		}
		m["hooks"] = append(hs, map[string]any{
			"type": "command", "command": wantHook, "timeout": 10,
		})
		return save(path, doc)
	}
	// No Bash matcher at all: make one, keeping any others.
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	pre, _ := hooks["PreToolUse"].([]any)
	hooks["PreToolUse"] = append(pre, map[string]any{
		"matcher": "Bash",
		"hooks": []any{map[string]any{
			"type": "command", "command": wantHook, "timeout": 10,
		}},
	})
	return save(path, doc)
}

// save writes the document back, after copying what was there beside it. The
// settings are not under version control on this machine — the two
// hand-made .bak files next to them are the evidence — so the copy is the only
// way back.
func save(path string, doc settingsDoc) error {
	if b, err := os.ReadFile(path); err == nil {
		bak := path + ".bak-" + timeNow().Format("20060102-150405")
		if err := os.WriteFile(bak, b, 0o600); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

var timeNow = time.Now
