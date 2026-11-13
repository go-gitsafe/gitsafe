package chainedgate

import (
	"strings"
	"testing"
)

// The write with no gate: `gh pr create --body-file F` needs prbody AND
// paritycheck text on F, joined by `&&`.
func TestRequireAllowsTheFullChain(t *testing.T) {
	for _, cmd := range []string{
		`prbody b.md --repo o/r && paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
		`prbody "$S/b.md" --repo o/r && paritycheck text "$S/b.md" && gh pr create --repo o/r --body-file "$S/b.md"`,
		`cd /some/where && prbody b.md --repo o/r && paritycheck text b.md && gh pr create --body-file=b.md`,
		`paritycheck text b.md && prbody b.md --repo o/r && gh pr create -R o/r -F b.md --title t`,
		`~/.local/bin/prbody b.md --repo o/r && ~/.local/bin/paritycheck text b.md && gh pr create --body-file b.md`,
		// Not this rule's: no body file, a read, and other writes.
		`gh pr view 42`,
		`gh pr create --help`,
		`gh pr create -h`,
		`ghissue o/r --new --body-file b.md`,
	} {
		if m := Require(cmd); m.Found() {
			t.Errorf("refused %q: %s", cmd, m.Why())
		}
	}
}

func TestRequireRefusesWhatLacksACheck(t *testing.T) {
	for name, cmd := range map[string]string{
		"neither":             `gh pr create --repo o/r --title t --body-file b.md`,
		"only prbody":         `prbody b.md --repo o/r && gh pr create --repo o/r --body-file b.md`,
		"only paritycheck":    `paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
		"another file":        `prbody a.md --repo o/r && paritycheck text a.md && gh pr create --repo o/r --body-file b.md`,
		"parity on a file":    `prbody b.md --repo o/r && paritycheck text a.md && gh pr create --repo o/r --body-file b.md`,
		"checks after":        `gh pr create --repo o/r --body-file b.md && prbody b.md --repo o/r && paritycheck text b.md`,
		"a semicolon":         `prbody b.md --repo o/r && paritycheck text b.md; gh pr create --repo o/r --body-file b.md`,
		"an or":               `prbody b.md --repo o/r && paritycheck text b.md || gh pr create --repo o/r --body-file b.md`,
		"paritycheck repo":    `prbody b.md --repo o/r && paritycheck repo b.md && gh pr create --repo o/r --body-file b.md`,
		"the file as a words": `echo prbody b.md && echo paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
	} {
		if m := Require(cmd); !m.Found() {
			t.Errorf("%s: allowed %q", name, cmd)
		}
	}
}

// Each half is named, so the refusal says what to add.
func TestRequireNamesTheMissingCheckAndTheLineToType(t *testing.T) {
	m := Require(`prbody b.md --repo o/r && gh pr create --repo o/r --title t --body-file b.md`)
	if !m.Found() || len(m.Checks) != 1 || !strings.Contains(m.Checks[0], "paritycheck text b.md") {
		t.Fatalf("%+v", m)
	}
	m = Require(`paritycheck text b.md && gh pr create --repo o/r --title t --body-file b.md`)
	if len(m.Checks) != 1 || !strings.Contains(m.Checks[0], "prbody b.md --repo o/r") {
		t.Fatalf("%+v", m)
	}
	m = Require(`gh pr create -R go-zpkg/zbk --title t --body-file /tmp/b.md`)
	want := `prbody /tmp/b.md --repo go-zpkg/zbk && paritycheck text /tmp/b.md && gh pr create -R go-zpkg/zbk --title t --body-file /tmp/b.md`
	if got := m.Instead(); got != want || len(m.Checks) != 2 {
		t.Errorf("Instead() = %q, want %q", got, want)
	}
	// The line it names passes its own rule: a refusal that names a line the
	// same rule refuses would send the author in a circle.
	if Require(want).Found() {
		t.Error("the line the refusal tells you to type is itself refused")
	}
	// A command naming no repository still gets a placeholder to fill in.
	if m := Require(`gh pr create --body-file b.md`); !strings.Contains(m.Instead(), "--repo <owner/repo>") {
		t.Errorf("%q", m.Instead())
	}
	if (Missing{}).String() != "" || (Missing{}).Found() {
		t.Error("the zero value is a refusal")
	}
	if !strings.Contains(m.Why(), "immediately before it") || !strings.Contains(m.String(), "--body-file") {
		t.Errorf("%q %q", m.Why(), m.String())
	}
}

// A heredoc that only QUOTES the forms is not a command.
func TestRequireIgnoresAQuotedHeredoc(t *testing.T) {
	cmd := "cat > note.md <<'EOF'\ngh pr create --body-file b.md\nEOF"
	if m := Require(cmd); m.Found() {
		t.Errorf("refused a quoted heredoc: %s", m.Why())
	}
}

func TestRequireReadsTheEqualsSpellings(t *testing.T) {
	m := Require(`gh pr create --repo=o/r --body-file=b.md`)
	if !m.Found() || m.Repo != "o/r" || m.File != "b.md" {
		t.Errorf("%+v", m)
	}
}

// ⛔ EVERY gh pr create NEEDS A BODY FILE: a body given any other way, or none,
// gives the checks nothing to run on.
func TestRequireRefusesAPullRequestWithNoBodyFile(t *testing.T) {
	for name, cmd := range map[string]string{
		"no body flag":    `gh pr create --title t`,
		"--body":          `gh pr create --title t --body "it fixes #3"`,
		"-b":              `gh pr create -t t -b "x"`,
		"--fill":          `gh pr create --fill`,
		"stdin":           `gh pr create --body-file -`,
		"checks, no file": `prbody b.md --repo o/r && paritycheck text b.md && gh pr create --title t --fill`,
	} {
		m := Require(cmd)
		if !m.Found() || !m.NoBody {
			t.Errorf("%s: %+v", name, m)
			continue
		}
		write := cmd[strings.LastIndex(cmd, " && ")+1:]
		if strings.HasPrefix(write, "&& ") {
			write = write[3:]
		}
		if want := "prbody <body-file> --repo <owner/repo> && paritycheck text <body-file> && " + write + " --body-file <body-file>"; m.Instead() != want {
			t.Errorf("%s: Instead() = %q, want %q", name, m.Instead(), want)
		}
		if !strings.Contains(m.Why(), "no `--body-file`") || !strings.Contains(m.String(), "with no `--body-file`") {
			t.Errorf("%s: %q %q", name, m.Why(), m.String())
		}
	}
}

// ⛔ A DIFFERENT SPELLING OF THE SAME COMMAND is the same command.
func TestRequireReadsDisguisedInvocations(t *testing.T) {
	for name, cmd := range map[string]string{
		"env":                  `env gh pr create --repo o/r --body-file b.md`,
		"env with assignments": `env -i GH_TOKEN=x -u FOO gh pr create --repo o/r --body-file b.md`,
		"command":              `command gh pr create --repo o/r --body-file b.md`,
		"exec":                 `exec gh pr create --repo o/r --body-file b.md`,
		"nohup":                `nohup gh pr create --repo o/r --body-file b.md`,
		"quoted letters":       `g"h" pr create --repo o/r --body-file b.md`,
		"quoted subcommand":    `gh p'r' "create" --repo o/r --body-file b.md`,
		"escaped":              `g\h pr create --repo o/r --body-file b.md`,
		"a path":               `/opt/homebrew/bin/gh pr create --repo o/r --body-file b.md`,
		"global repo flag":     `gh -R o/r pr create --body-file b.md`,
		"global --repo flag":   `gh --repo o/r pr create --body-file b.md`,
		"global --repo=":       `gh --repo=o/r pr create --body-file b.md`,
		"global hostname":      `gh --hostname github.com pr create --repo o/r --body-file b.md`,
		"bash -c":              `bash -c "gh pr create --repo o/r --body-file b.md"`,
		"sh -lc":               `sh -lc 'gh pr create --repo o/r --body-file b.md'`,
		"zsh -c":               `zsh -c "gh pr create --repo o/r --body-file b.md"`,
		"nested shells":        `bash -c "sh -c 'gh pr create --repo o/r --body-file b.md'"`,
		"env bash -c":          `env X=1 bash -c "gh pr create --repo o/r --body-file b.md"`,
		"bash -c no body":      `bash -c "gh pr create --fill"`,
		"quoted file":          `gh pr create --repo o/r --body-file "b.md"`,
	} {
		if m := Require(cmd); !m.Found() {
			t.Errorf("%s: allowed %q", name, cmd)
		}
	}
	// The same spellings behind the full chain are allowed: the rule reads the
	// command, it does not forbid the spelling.
	for name, cmd := range map[string]string{
		"env":      `prbody b.md --repo o/r && paritycheck text b.md && env gh pr create --repo o/r --body-file b.md`,
		"quoted":   `prbody b.md --repo o/r && paritycheck text b.md && g"h" pr create --repo o/r --body-file b.md`,
		"global":   `prbody b.md --repo o/r && paritycheck text b.md && gh -R o/r pr create --body-file b.md`,
		"inside":   `bash -c "prbody b.md --repo o/r && paritycheck text b.md && gh pr create --repo o/r --body-file b.md"`,
		"prefixed": `GH_TOKEN=x prbody b.md --repo o/r && env paritycheck text b.md && command gh pr create --repo o/r --body-file b.md`,
	} {
		if m := Require(cmd); m.Found() {
			t.Errorf("%s: refused %q: %s", name, cmd, m.Why())
		}
	}
	// Recursion is bounded.
	deep := `gh pr create --repo o/r --body-file b.md`
	for i := 0; i < 8; i++ {
		deep = `bash -c ` + jsonQuote(deep)
	}
	if m := Require(deep); m.Found() {
		t.Error("a script nested past the bound was read")
	}
}

func jsonQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// ⛔ THE FILE MUST NOT CHANGE BETWEEN THE CHECK AND THE CREATE. The checks are a
// block directly before the create; anything between them, or a redirection
// into the file, publishes a body no check saw.
func TestRequireRefusesWhatTouchesTheFileInBetween(t *testing.T) {
	for name, cmd := range map[string]string{
		"a copy between":    `prbody b.md --repo o/r && paritycheck text b.md && cp evil.md b.md && gh pr create --repo o/r --body-file b.md`,
		"a copy first":      `prbody b.md --repo o/r && cp evil.md b.md && paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
		"any command":       `prbody b.md --repo o/r && paritycheck text b.md && true && gh pr create --repo o/r --body-file b.md`,
		"an echo":           `prbody b.md --repo o/r && paritycheck text b.md && echo "x" >> b.md && gh pr create --repo o/r --body-file b.md`,
		"redirect in check": `prbody b.md --repo o/r > b.md && paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
		"redirect >F":       `prbody b.md --repo o/r && paritycheck text b.md >b.md && gh pr create --repo o/r --body-file b.md`,
		"redirect 2>F":      `prbody b.md --repo o/r && paritycheck text b.md 2>b.md && gh pr create --repo o/r --body-file b.md`,
		"redirect &>F":      `prbody b.md --repo o/r &> b.md && paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
		"a pipeline":        `prbody b.md --repo o/r | tee b.md && paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
		"a pipeline tail":   `prbody b.md --repo o/r && paritycheck text b.md | sh && gh pr create --repo o/r --body-file b.md`,
	} {
		if m := Require(cmd); !m.Found() {
			t.Errorf("%s: allowed %q", name, cmd)
		}
	}
	// A redirection to ANOTHER file is fine, and so is anything BEFORE the block.
	for name, cmd := range map[string]string{
		"output elsewhere": `prbody b.md --repo o/r > out.txt 2>&1 && paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
		"cd first":         `cd /w && git status && prbody b.md --repo o/r && paritycheck text b.md && gh pr create --repo o/r --body-file b.md`,
		"either order":     `paritycheck text b.md && prbody b.md --repo o/r && gh pr create --repo o/r --body-file b.md`,
	} {
		if m := Require(cmd); m.Found() {
			t.Errorf("%s: refused %q: %s", name, cmd, m.Why())
		}
	}
}

func TestWordSplittingAndNormalisation(t *testing.T) {
	for in, want := range map[string]string{
		`g"h" p'r'`:           "gh|pr",
		`a\ b c`:              "a b|c",
		`"a\"b" 'c d'`:        `a"b|c d`,
		"x\ty\nz":             "x|y|z",
		`"" x`:                "|x",
		`trailing\`:           `trailing\`,
		`"a\nb"`:              `a\nb`,
		`env -i X=1 -u Y gh`:  "gh",
		`FOO=1 BAR=2 env gh`:  "gh",
		`command -v gh`:       "gh",
		`nohup time env gh x`: "gh|x",
	} {
		ws := splitWords(in)
		if strings.HasPrefix(in, "env") || strings.Contains(in, "FOO=") || strings.HasPrefix(in, "command") || strings.HasPrefix(in, "nohup") {
			ws = normalise(in)
		}
		if got := strings.Join(ws, "|"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	// A shell that runs a file, or a flag without a script, is not a script.
	for _, cmd := range []string{`bash script.sh`, `bash -x script.sh`, `bash -c`, `bash --login -c`, `sh -e`, `zsh`} {
		if s, ok := shellScript(normalise(cmd)); ok {
			t.Errorf("%q read as the script %q", cmd, s)
		}
	}
	if s, ok := shellScript(normalise(`bash --norc -c "x y"`)); !ok || s != "x y" {
		t.Errorf("%q %v", s, ok)
	}
}

// ⛔ `gh` IS FOUND WHEREVER IT SITS: a wrapper this rule has never heard of is
// still a wrapper. The checks in front of it make it legitimate.
func TestRequireFindsGhBehindAnyWrapper(t *testing.T) {
	const create = `gh pr create --repo o/r --title t --body-file b.md`
	for name, cmd := range map[string]string{
		"nice":      `nice -n 5 ` + create,
		"timeout":   `timeout 30 ` + create,
		"sudo":      `sudo ` + create,
		"find":      `find . -maxdepth 0 -exec ` + create + ` \;`,
		"xargs":     `xargs -a list.txt ` + create,
		"xargs -I":  `echo x | xargs -I{} ` + create,
		"time -p":   `time -p ` + create,
		"ionice":    `ionice -c3 ` + create,
		"a wrapper": `anything-at-all --flag ` + create,
		"echo":      `echo ` + create,
	} {
		m := Require(cmd)
		if !m.Found() || m.File != "b.md" {
			t.Errorf("%s: allowed %q (%+v)", name, cmd, m)
		}
		checked := `prbody b.md --repo o/r && paritycheck text b.md && ` + cmd
		if name != "xargs -I" && Require(checked).Found() {
			t.Errorf("%s: refused behind the full chain: %q", name, checked)
		}
	}
	// A different gh subcommand, and a path that merely contains "gh", are not it.
	for _, cmd := range []string{`nice gh pr view 4`, `timeout 5 gh issue create --title t`, `ls high/gh.txt`, `sudo apt install ghc`} {
		if m := Require(cmd); m.Found() {
			t.Errorf("refused %q: %s", cmd, m.Why())
		}
	}
}

// ⛔ eval builds its command at run time: refused, quoted or not, even behind
// the checks, because they cannot have seen what it will run.
func TestRequireRefusesEval(t *testing.T) {
	for name, cmd := range map[string]string{
		"quoted":    `eval "gh pr create --repo o/r --title t --body-file b.md"`,
		"unquoted":  `eval gh pr create --repo o/r --title t --body-file b.md`,
		"single":    `eval 'gh pr create --repo o/r --body-file b.md'`,
		"behind":    `prbody b.md --repo o/r && paritycheck text b.md && eval "gh pr create --repo o/r --body-file b.md"`,
		"with env":  `GH_TOKEN=x eval "gh pr create --repo o/r --body-file b.md"`,
		"a prefix":  `env eval gh -R o/r pr create --body-file b.md`,
		"in a bash": `bash -c 'eval "gh pr create --repo o/r --body-file b.md"'`,
	} {
		m := Require(cmd)
		if !m.Found() || !m.Eval {
			t.Errorf("%s: %q gave %+v", name, cmd, m)
			continue
		}
		if !strings.Contains(m.Why(), "eval") || !strings.Contains(m.String(), "gh pr create") || !strings.Contains(m.Instead(), "no eval") {
			t.Errorf("%s: %q %q %q", name, m.Why(), m.String(), m.Instead())
		}
	}
	// eval of something else is not this rule's.
	for _, cmd := range []string{`eval "$(ssh-agent -s)"`, `eval echo hello`, `eval`} {
		if m := Require(cmd); m.Found() {
			t.Errorf("refused %q", cmd)
		}
	}
}
