// Command guard-read is a PreToolUse hook for the Read and Bash tools. It
// refuses a call that would DISPLAY a file holding a credential, judged by
// opening the file and scanning its content, not by its name.
//
// Install (matchers Read and Bash) in ~/.claude/settings.json:
//
//	{"matcher": "Read|Bash", "hooks": [{"type": "command", "command": "guard-read"}]}
//
// The refusal names the file and the KIND found, never the value, and points
// to `secretpeek`. When it cannot read something it allows the call and says so
// on stderr: a refusal is not a verdict about a file never seen.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-gitsafe/gitsafe/secretread"
)

type payload struct {
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		FilePath string `json:"file_path"`
		Command  string `json:"command"`
	} `json:"tool_input"`
}

type decision struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// osExit and marshal are variables so a test can reach main's exit and the
// marshal failure, which no real input produces.
var (
	osExit  = os.Exit
	marshal = json.Marshal
)

func main() {
	home, _ := os.UserHomeDir()
	osExit(run(os.Stdin, os.Stdout, os.Stderr, home))
}

// run always exits 0: the answer is in the JSON, and a guard that failed
// closed on its own bug would block every read on the machine.
func run(stdin io.Reader, stdout, stderr io.Writer, home string) int {
	// A read error leaves what was read, which does not parse: no opinion.
	b, _ := io.ReadAll(stdin)
	var p payload
	if json.Unmarshal(b, &p) != nil {
		return 0
	}
	var refused *secretread.Verdict
	var unreadable []secretread.Verdict
	switch p.ToolName {
	case "Read":
		v := secretread.CheckFile(secretread.Expand(p.ToolInput.FilePath, home, p.Cwd), home)
		if v.Err != nil {
			unreadable = append(unreadable, v)
		}
		if v.Refused() {
			refused = &v
		}
	case "Bash":
		refused, unreadable = secretread.CheckCommand(p.ToolInput.Command, p.Cwd, home)
	default:
		return 0
	}
	for _, u := range unreadable {
		fmt.Fprintf(stderr, "guard-read: cannot read %s (%v); allowing, content NOT checked\n", u.Path, u.Err)
	}
	if refused == nil {
		return 0
	}
	var d decision
	d.HookSpecificOutput.HookEventName = "PreToolUse"
	d.HookSpecificOutput.PermissionDecision = "deny"
	d.HookSpecificOutput.PermissionDecisionReason = Reason(*refused)
	out, err := marshal(d)
	if err != nil {
		return 0
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// Reason builds the refusal text. It is built from a Verdict, which has no
// field that could hold a secret value.
func Reason(v secretread.Verdict) string {
	what := "holds a credential (" + strings.Join(v.Kinds, ", ") + ")"
	if v.Line > 0 {
		what += fmt.Sprintf(" at line %d", v.Line)
	}
	if v.ByPath != "" {
		what = "is " + v.ByPath
	}
	return fmt.Sprintf("Refused: %s %s. Displaying it would put the secret in the transcript.\n\n"+
		"Inspect its properties instead, no value is printed:\n"+
		"  secretpeek %s\n"+
		"  secretpeek --key <non-secret key, e.g. base_url> %s\n"+
		"The value is never needed on screen: a program reads the file itself.",
		v.Path, what, v.Path, v.Path)
}
