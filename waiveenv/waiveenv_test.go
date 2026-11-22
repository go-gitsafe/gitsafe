package waiveenv

import (
	"strings"
	"testing"
)

// The names are the three that fleet gates read today (grepped out of
// go-fleettools/fleettools on 2026-11-22), spelled out here and nowhere in a
// shell command: a command that merely typed them was refused by the deny rules
// in the operator's settings, which is how this rule came to be asked for.
const (
	mut = "MUTSWEEP_SKIP"
	tid = "MODTIDY_WAIVE"
	bld = "BUILDSCAN_SKIP"
)

// ⛔ Every row refused here was, or is one step from, a measured bypass. The
// permissions.deny wildcard Bash(*VAR=*) did NOT fire on the first of them
// (2026-11-22, go-fft docs round, 4 commands with is_error:false): Claude Code
// strips leading assignments from each sub-command before matching, so a
// pattern on the assignment never sees it.
func TestEveryPositionOfAFleetWaiverIsRefused(t *testing.T) {
	for name, cmd := range map[string]string{
		"the measured form: cd then assignment then gitpush": "cd /Users/u/GIT/localhost/go-fft-docs && " + tid + `="fleettools#294" gitpush -u origin topic`,
		"a plain prefix":                   mut + "=1 gitpush origin topic",
		"a prefix before git commit":       tid + `="x" git commit -F msg.txt`,
		"after a semicolon":                "git add -A; " + bld + "=1 gitpush origin topic",
		"after &&":                         "git add -A && " + bld + "=1 gitpush origin topic",
		"after ||":                         "false || " + bld + "=1 gitpush origin topic",
		"after a newline":                  "git add -A\n" + bld + "=1 gitpush origin topic",
		"after a pipe":                     "echo x | " + bld + "=1 cat",
		"two assignments, waiver second":   "FOO=1 " + mut + "=1 gitpush o t",
		"two assignments, waiver first":    mut + "=1 FOO=1 gitpush o t",
		"env":                              "env " + mut + "=1 gitpush o t",
		"env with an option first":         "env -i FOO=1 " + mut + "=1 gitpush o t",
		"env after cd":                     "cd /x && env " + mut + "=1 gitpush o t",
		"env with a quoted assignment":     `env "` + mut + `=1" gitpush o t`,
		"sudo":                             "sudo " + mut + "=1 gitpush o t",
		"sudo with a user":                 "sudo -u bob " + mut + "=1 gitpush o t",
		"timeout":                          "timeout 600 " + mut + "=1 gitpush o t",
		"nohup":                            "nohup " + mut + "=1 gitpush o t",
		"command":                          "command " + mut + "=1 gitpush o t",
		"time":                             "time " + mut + "=1 gitpush o t",
		"nice":                             "nice -n 5 " + mut + "=1 gitpush o t",
		"export then use":                  "export " + mut + "=1 && gitpush o t",
		"export with other names":          "export FOO=1 " + tid + "=1; gitpush o t",
		"declare -x":                       "declare -x " + tid + "=1; gitpush o t",
		"a subshell":                       "(" + mut + "=1 gitpush o t)",
		"a subshell after cd":              "cd /x && (" + mut + "=1 gitpush o t)",
		"a brace group":                    "{ " + mut + "=1 gitpush o t; }",
		"if then":                          "if true; then " + mut + "=1 gitpush o t; fi",
		"bash -c":                          `bash -c "` + mut + `=1 gitpush o t"`,
		"sh -c single quotes":              `sh -c '` + mut + `=1 gitpush o t'`,
		"zsh -c with cd":                   `zsh -c 'cd /x && ` + tid + `=1 gitpush o t'`,
		"eval string":                      `eval "` + mut + `=1 gitpush o t"`,
		"eval bare words":                  "eval " + mut + "=1 gitpush o t",
		"command substitution":             "echo $(" + mut + "=1 gitpush o t)",
		"backticks":                        "echo `" + mut + "=1 gitpush o t`",
		"substitution in double quotes":    `echo "$(` + mut + `=1 gitpush o t)"`,
		"empty value":                      mut + "= gitpush o t",
		"value zero is still a waiver":     mut + "=0 gitpush o t",
		"a quoted value":                   mut + `='1' gitpush o t`,
		"a full path to the program":       mut + "=1 ~/.local/bin/gitpush o t",
		"a variable no fleet gate has yet": "NEWGATE_SKIP=1 gitpush o t",
		"another waiver suffix":            "NEWGATE_WAIVE=1 gitpush o t",
		"the waiver suffix again":          "NEWGATE_WAIVER=x gitpush o t",
		"background":                       mut + "=1 gitpush o t &",
		"xargs":                            "echo x | xargs " + mut + "=1 gitpush",
		"a heredoc that is not quoted":     "cat <<EOF\n" + mut + "=1 gitpush o t\nEOF",
	} {
		f := Check(cmd)
		if !f.Found() {
			t.Errorf("%s: not refused: %q", name, cmd)
			continue
		}
		if f.Var == "" || f.Match == "" {
			t.Errorf("%s: the finding must name the variable and point at the text: %+v", name, f)
		}
	}
}

// The other half, which keeps the rule from being deleted: writing ABOUT a
// waiver, and every ordinary assignment, are left alone.
func TestOrdinaryCommandsAreNotRefused(t *testing.T) {
	for name, cmd := range map[string]string{
		"plain gitpush":                     "gitpush -u origin topic",
		"an ordinary assignment":            "GOFLAGS=-count=1 go build ./...",
		"several ordinary assignments":      "CGO_ENABLED=0 GOOS=linux go build ./...",
		"env with ordinary names":           "env FOO=1 BAR=2 gitpush o t",
		"a commit message that names it":    "git commit -m 'stop using " + mut + "=1 on pushes'",
		"a double-quoted message":           `git commit -m "no more ` + tid + `=1 here"`,
		"a tell message":                    `agentsync tell peer "do not set ` + bld + `=1"`,
		"a quoted heredoc":                  "cat <<'EOF'\n" + mut + "=1 gitpush o t\nEOF",
		"a comment":                         "git status # " + mut + "=1 gitpush",
		"an argument that has an equals":    "go test -run Foo -count=1 ./...",
		"a flag with a skip-like name":      "go test -skip=Slow ./...",
		"a variable merely ending skip":     "SKIPPED=1 go test ./...",
		"a lowercase look-alike":            "mutsweep_skip=1 true",
		"a name that only contains skip":    "SKIP_THIS=1 true",
		"a suffix with no name":             "_SKIP=1 true",
		"a path containing the name":        "cat docs/" + mut + "=1.txt",
		"reading the variable":              `echo "$` + mut + `"`,
		"unsetting it":                      "unset " + mut,
		"an argument after the command":     "echo " + mut + "=1",
		"grep for the name":                 "git grep -n " + mut + "=1",
		"export of an ordinary name":        "export GOFLAGS=-count=1 && go build ./...",
		"a shell -c without a waiver":       `bash -c "gitpush o t"`,
		"a quoted word that is not a shell": `printf '%s\n' "` + mut + `=1 is refused"`,
		"empty":                             "",
		"only spaces":                       "   ",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("%s: refused: %q (%+v)", name, cmd, f)
		}
	}
}

func TestTheRefusalNamesTheVariableAndWhatToDoInstead(t *testing.T) {
	f := Check("cd /x && " + tid + "=1 gitpush o t")
	if f.Var != tid {
		t.Fatalf("Var = %q, want %q", f.Var, tid)
	}
	if !strings.Contains(f.Match, tid+"=1") {
		t.Errorf("Match = %q", f.Match)
	}
	if !strings.Contains(f.Why(), "waive") {
		t.Errorf("Why = %q", f.Why())
	}
	for _, want := range []string{"fix", "ghissue"} {
		if !strings.Contains(Advice, want) {
			t.Errorf("Advice does not say %q:\n%s", want, Advice)
		}
	}
}

// The first waiver found is the one named, wherever it sits, even behind an
// ordinary one.
func TestTheFirstWaiverIsNamed(t *testing.T) {
	if f := Check("FOO=1 " + bld + "=1 " + mut + "=1 gitpush"); f.Var != bld {
		t.Errorf("Var = %q, want %q", f.Var, bld)
	}
}

func TestIsWaiverName(t *testing.T) {
	for name, want := range map[string]bool{
		mut: true, tid: true, bld: true,
		"X_SKIP": true, "X_WAIVE": true, "X_WAIVER": true, "A1_B2_SKIP": true,
		"SKIP": false, "_SKIP": false, "X_SKIPPED": false, "X_SKIP_NOT": false,
		"x_skip": false, "GOFLAGS": false, "": false, "X_WAIVED": false,
	} {
		if got := IsWaiverName(name); got != want {
			t.Errorf("IsWaiverName(%q) = %v, want %v", name, got, want)
		}
	}
}

func FuzzCheck(f *testing.F) {
	for _, s := range []string{"", mut + "=1 gitpush", "env " + mut + "=1 x", `bash -c "` + mut + `=1 x"`, "$(", "`", `"`, "'", "((", "))"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = Check(s) // must not panic or hang on any input
	})
}
