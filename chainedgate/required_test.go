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
		`gh pr create --title t`,
		`gh pr view 42`,
		`gh pr create --body-file -`,
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
	if !strings.Contains(m.Why(), "without first running") || !strings.Contains(m.String(), "--body-file") {
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
