package secretarg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⛔ This test exists because the README's install list named 9 of the 15
// commands. The absences that mattered:
//
//   - `git-credential-tokenfile`, which `gitpush` REQUIRES — it names
//     `tokenfile` as the helper and git resolves that to
//     `git-credential-tokenfile` on the PATH. Without it nothing serves the
//     token and the first push fails, which is exactly the moment somebody puts
//     a credential in a URL.
//   - `ghrelease`, to which the README devotes a whole section.
//
// A command nobody installs is a command that does not exist, and the list goes
// stale in silence every time a command is added.
func TestTheInstallListNamesEveryCommand(t *testing.T) {
	root := moduleRoot(t)
	ents, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Skipf("no cmd directory: %v", err)
	}
	var cmds []string
	for _, e := range ents {
		if e.IsDir() {
			cmds = append(cmds, e.Name())
		}
	}
	if len(cmds) < 5 {
		t.Fatalf("only %d command(s) seen: reading cmd/ returns almost nothing", len(cmds))
	}

	b, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Skipf("no README: %v", err)
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`cmd/([a-z0-9-]+)@latest`).FindAllStringSubmatch(string(b), -1) {
		listed[m[1]] = true
	}
	// ⭐ The other half: if the pattern stops recognising an install line it
	// finds nothing, and this test would report "everything is missing" — or,
	// with the comparison the other way round, "nothing is missing". Both are
	// wrong for the same reason.
	if len(listed) == 0 {
		t.Fatal("no `go install .../cmd/X@latest` line recognised in the README: " +
			"the pattern no longer reads the list, and this test checks nothing")
	}

	var missing []string
	for _, c := range cmds {
		if !listed[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d command(s) the README's install list does not name: %s\n  "+
			"A command nobody installs is a command that does not exist.",
			len(missing), strings.Join(missing, " "))
	}
	for name := range listed {
		if _, err := os.Stat(filepath.Join(root, "cmd", name)); err != nil {
			t.Errorf("the README installs cmd/%s, which does not exist", name)
		}
	}
	t.Logf("%d commands, %d named in the list", len(cmds), len(listed))
}
