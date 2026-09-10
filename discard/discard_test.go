package discard

import (
	"strings"
	"testing"
)

// The three spellings that actually destroyed work here, and the chain the first
// one hid in.
func TestRefusesWhatDestroyedWork(t *testing.T) {
	for name, cmd := range map[string]string{
		"the chain that erased a fix": `git checkout -q -- . 2>/dev/null; git status --porcelain | head -4; git add -A && git commit -m x`,
		"a file from another branch":  `git checkout main -- engine/font_test.go`,
		"checkout with no separator":  `git checkout .`,
		"restore, the modern name":    `git restore internal/x.go`,
		"restore the whole tree":      `git restore .`,
		"reset --hard":                `git reset --hard origin/main`,
		"clean -fd":                   `git clean -fd`,
		"clean --force":               `git clean --force -x`,
		"behind an env assignment":    `GIT_DIR=.git git checkout -- .`,
		"with git's own -C":           `git -C /some/repo checkout -- .`,
		"by absolute path":            `/usr/bin/git restore .`,
		"second in a chain":           `cd /tmp/x && git reset --hard`,
	} {
		if f := Check(cmd); !f.Found() {
			t.Errorf("%s: not refused — %s", name, cmd)
		} else if f.Why == "" || f.Match == "" {
			t.Errorf("%s: refusal says nothing actionable: %+v", name, f)
		}
	}
}

// The half that keeps a guard from being worked around. Every one of these is
// ordinary work, and refusing any of them would get the guard switched off.
func TestLetsOrdinaryWorkThrough(t *testing.T) {
	for name, cmd := range map[string]string{
		"creating a branch":       `git checkout -b feat/thing`,
		"switching to one":        `git checkout main`,
		"switching back":          `git checkout -`,
		"switch, the modern name": `git switch main`,
		"unstaging only":          `git restore --staged internal/x.go`,
		"unstaging the lot":       `git restore --staged .`,
		"a soft reset":            `git reset --soft HEAD~1`,
		"a mixed reset":           `git reset HEAD~1`,
		"unstaging by path":       `git reset HEAD -- x.go`,
		"a dry-run clean":         `git clean -nd`,
		"stashing, the safe way":  `git stash push -- x.go`,
		"reading, not writing":    `git status --porcelain; git diff --stat`,
		"a different program":     `docker checkout -- .`,
		"a word in a message":     `git commit -m "never run git checkout -- . in a chain"`,
		"restore with no path":    `git restore`,
		"nothing to do with git":  `echo git reset --hard`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("%s: wrongly refused (%s): %s", name, f.Rule, cmd)
		}
	}
}

// Writing ABOUT the forbidden form is not the forbidden form. A quoted heredoc
// is literal, so a commit message or a note that quotes it must go through — the
// rule has to be writable down, including in this repository's own history.
func TestWritingAboutItIsNotDoingIt(t *testing.T) {
	cmd := "git commit -q -F - <<'EOF'\nDo not put git checkout -- . in a chain\nit erased a fix once\nEOF"
	if f := Check(cmd); f.Found() {
		t.Errorf("a quoted heredoc was refused (%s): %s", f.Rule, cmd)
	}
	// An UNQUOTED heredoc does expand, so its body is scanned.
	cmd = "cat <<EOF\ngit reset --hard\nEOF"
	if f := Check(cmd); !f.Found() {
		t.Error("an unquoted heredoc body was not scanned")
	}
}

// The escape the refusal names must actually open. It is honoured as a PREFIX on
// the command, because the guard runs as a pre-tool hook and reads the command as
// text: the environment the command would run with never reaches it. Shipped the
// other way round, the escape did nothing and the refusal could not be got past —
// found the first time a legitimate discard needed it.
func TestTheEscapeTheMessageNamesActuallyWorks(t *testing.T) {
	for _, cmd := range []string{
		`GITSAFE_ALLOW_DISCARD=1 git restore playground/x.png`,
		`GITSAFE_ALLOW_DISCARD=yes git checkout -- .`,
		`GITSAFE_ALLOW_DISCARD=1 git reset --hard`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("the escape did not open (%s): %s", f.Rule, cmd)
		}
	}
	// And it is an ACT, not a word: an empty or zero value is not saying yes, and
	// the escape on one command in a chain does not cover the next.
	for _, cmd := range []string{
		`GITSAFE_ALLOW_DISCARD= git reset --hard`,
		`GITSAFE_ALLOW_DISCARD=0 git reset --hard`,
		`GITSAFE_ALLOW_DISCARD=1 git status; git reset --hard`,
	} {
		if f := Check(cmd); !f.Found() {
			t.Errorf("the escape leaked: %s", cmd)
		}
	}
}

func TestAdviceNamesTheSafeThing(t *testing.T) {
	for _, want := range []string{"git stash push", "GITSAFE_ALLOW_DISCARD"} {
		if !strings.Contains(Advice, want) {
			t.Errorf("Advice does not mention %q", want)
		}
	}
}

func TestZeroFindingIsNotFound(t *testing.T) {
	if (Finding{}).Found() {
		t.Error("the zero Finding reads as found")
	}
}
