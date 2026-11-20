package literaltick

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ⛔ THE ORACLE RUNS THE SHELL THAT RUNS THE COMMAND (#156). The Bash tool does not
// start bash: it starts `zsh -c 'source <snapshot> … && \builtin setopt … && eval
// <command>'`. These tests pin that wrapper so a drift fails CI loudly instead of
// silently moving what the oracle asks.

// The setopt line in the code is the line in the golden file, byte for byte. The
// file is the capture; the constant is what the oracle runs; a change to either
// without the other is this failure.
func TestTheProductionWrapperIsPinned(t *testing.T) {
	b, err := os.ReadFile("testdata/production-wrapper.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != productionSetopt {
		t.Errorf("testdata/production-wrapper.txt and productionSetopt differ:\n  file: %s\n  code: %s\nre-capture the live wrapper with `ps -ww -o command= -p $$` and update BOTH", got, productionSetopt)
	}
	for _, opt := range []string{"NO_EXTENDED_GLOB", "NO_BARE_GLOB_QUAL", "NO_BRACE_CCL", "NO_MAGIC_EQUAL_SUBST"} {
		if !strings.Contains(productionSetopt, opt) {
			t.Errorf("the pinned line lacks %s", opt)
		}
	}
}

// What the pin means in zsh: the four options are really off under the wrapper
// (and extendedglob is on without it), the command is evaluated with its own
// quotes intact, and the zsh is the 5.x the line was captured on.
func TestTheProductionWrapperDoesWhatItIsPinnedFor(t *testing.T) {
	var zsh string
	for _, s := range oracleShells(t) {
		if isZsh(s) {
			zsh = s
		}
	}
	if zsh == "" {
		t.Skip("zsh is not installed here")
	}
	run := func(script string) string {
		c := exec.Command(zsh, "-c", script)
		c.Dir = t.TempDir()
		c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + c.Dir}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	ver := run("print -r -- $ZSH_VERSION")
	t.Logf("zsh %s", ver)
	if !strings.HasPrefix(ver, "5.") {
		t.Errorf("zsh %s: the wrapper was captured on 5.9; re-measure the oracle before accepting another major", ver)
	}
	const show = `print -r -- ${options[extendedglob]} ${options[bareglobqual]} ${options[braceccl]} ${options[magicequalsubst]}`
	if got := run(productionScript(show)); got != "off off off off" {
		t.Errorf("under the wrapper the four options read %q, want all off", got)
	}
	if got := run(show); got == "off off off off" {
		t.Errorf("without the wrapper the options already read all off: the pin proves nothing")
	}
	if got := run(productionScript(`print -r -- "it's" 'a b'`)); got != "it's a b" {
		t.Errorf("the eval quoting changed the command: %q", got)
	}
}
