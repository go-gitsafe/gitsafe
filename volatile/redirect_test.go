package volatile

import "testing"

func TestRedirectRefuses(t *testing.T) {
	log := scratch + "/probe.txt"
	cases := []struct {
		name, cmd, cwd, dir string
	}{
		{"the 2026-11-11 incident", "gotest ./interp > " + log + " 2>&1", durable, log},
		{"append", "go test ./... >> " + log, durable, log},
		{"stderr only", "go test ./... 2> " + log, durable, log},
		{"stdout by number", "go test ./... 1>" + log, durable, log},
		{"both, ampersand form", "go test ./... &>" + log, durable, log},
		{"both, ampersand form with space", "go test ./... &> " + log, durable, log},
		{"relative in a volatile cwd", "go test ./... > probe.txt", scratch, log},
		{"after a cd", "cd " + scratch + " && go test ./... > probe.txt 2>&1", durable, log},
		{"redirect first", "> " + log + " go test ./...", durable, log},
		{"in the tmpdir", "go test ./... > $TMPDIR/p.txt", durable, "/var/folders/xy/T/p.txt"},
		{"braced tmpdir", "go test ./... > ${TMPDIR}/p.txt", durable, "/var/folders/xy/T/p.txt"},
		{"second command of a chain", "make; go test ./... > /tmp/p.txt", durable, "/tmp/p.txt"},
		{"tee", "go test -v ./... | tee -a " + log, durable, log},
		{"go build -o", "go build -o " + scratch + "/bin ./cmd/x", durable, scratch + "/bin"},
		{"coverprofile equals", "go test -coverprofile=/tmp/c.out ./...", durable, "/tmp/c.out"},
		{"cpuprofile spaced", "go test --cpuprofile /tmp/cpu.out ./...", durable, "/tmp/cpu.out"},
		{"redirect after a duplication", "go test ./... 2>&1 > /tmp/p.txt", durable, "/tmp/p.txt"},
		{"escape set to 0 is no escape", Escape + "=0 go test > /tmp/p.txt", durable, "/tmp/p.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := CheckRedirect(c.cmd, c.cwd, home, tmpdir, true)
			if !f.Found() {
				t.Fatalf("CheckRedirect(%q) in %s: not refused", c.cmd, c.cwd)
			}
			if f.Rule != "redirect" || f.Dir != c.dir {
				t.Errorf("got rule %q dir %q, want redirect %q", f.Rule, f.Dir, c.dir)
			}
			if f.Match == "" || f.Why == "" {
				t.Errorf("finding without match or reason: %+v", f)
			}
		})
	}
}

func TestRedirectAllows(t *testing.T) {
	log := scratch + "/probe.txt"
	cases := []struct {
		name, cmd, cwd string
		background     bool
	}{
		{"the same command in the foreground", "gotest ./interp > " + log + " 2>&1", durable, false},
		{"foreground tee", "go test ./... | tee " + log, durable, false},
		{"foreground profile", "go test -coverprofile=/tmp/c.out ./...", durable, false},
		{"background to a durable file", "gotest ./interp > " + durable + "/probe.txt 2>&1", scratch, true},
		{"background, home-relative", "gotest ./interp > ~/Library/Application\\ Support/x/log", scratch, true},
		{"background, $HOME", "gotest ./interp > $HOME/log.txt", scratch, true},
		{"background to a relative durable file", "gotest ./interp > probe.txt", durable, true},
		{"background to /var/tmp", "gotest ./interp > /var/tmp/p.txt", durable, true},
		{"background to /dev/null", "gotest ./interp > /dev/null 2>&1", scratch, true},
		{"duplication writes nowhere", "gotest ./interp 2>&1", scratch, true},
		{"duplication to stderr", "echo x >&2", scratch, true},
		{"process substitution", "gotest ./interp > >(cat)", scratch, true},
		{"a quoted > is not a redirect", `echo "a>b"`, scratch, true},
		{"tmpdir unset", "gotest ./interp > $TMPDIR/p.txt", durable, true},
		{"no output at all", "gotest ./interp", scratch, true},
		{"cd home, then a durable file", "cd && gotest ./interp > log.txt", scratch, true},
		{"ssh -o is an option", "ssh -o StrictHostKeyChecking=no host", scratch, true},
		{"flag with no value", "go test -coverprofile", scratch, true},
		{"flag followed by a flag", "go test -o -v ./...", scratch, true},
		{"flag with an empty value", "go test -o= ./...", scratch, true},
		{"unrelated flag", "go test -v ./...", scratch, true},
		{"-o as the program's first word", "-o /tmp/x", durable, true},
		{"tee to a durable file", "go test ./... | tee " + durable + "/log", scratch, true},
		{"escape", Escape + "=1 gotest ./interp > " + log, durable, true},
		{"only an assignment", "X=1", scratch, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			td := tmpdir
			if c.name == "tmpdir unset" {
				td = ""
			}
			if f := CheckRedirect(c.cmd, c.cwd, home, td, c.background); f.Found() {
				t.Errorf("CheckRedirect(%q) in %s, background=%v: refused: %+v", c.cmd, c.cwd, c.background, f)
			}
		})
	}
}

func TestRedirectKeepsTheOtherRuleWorking(t *testing.T) {
	// scan is shared: Check must still see a clone after the refactor.
	if f := Check("cd "+scratch+" && git clone u", durable, home, tmpdir); !f.Found() {
		t.Error("Check no longer refuses a clone into the scratchpad")
	}
}

// The helpers the two rules share, each branch asked directly.
func TestSharedHelpers(t *testing.T) {
	if f := Check("git -c a=b commit -m x", durable, home, tmpdir); f.Found() {
		t.Errorf("a durable commit with -c refused: %+v", f)
	}
	if got := cloneDest(nil, durable, home, true); got != "" {
		t.Errorf("cloneDest with no arguments = %q", got)
	}
	if got := cloneDest([]string{"host:repo"}, durable, home, true); got != durable+"/repo" {
		t.Errorf("cloneDest of host:repo = %q", got)
	}
	if got := positionals([]string{"a", "--", "-b"}, nil); len(got) != 2 || got[1] != "-b" {
		t.Errorf("positionals after -- = %v", got)
	}
	if resolve(durable, home, "~") != home || resolve(durable, home, "$HOME") != home {
		t.Error("~ and $HOME must resolve to home")
	}
	if base("/usr/bin/git") != "git" || base("git") != "git" {
		t.Error("base")
	}
}

func TestOutputsOfATeeWithoutFiles(t *testing.T) {
	if got := outputs([]string{"tee", "-a"}); len(got) != 0 {
		t.Errorf("tee with no file wrote somewhere: %v", got)
	}
}
