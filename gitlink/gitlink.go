// Package gitlink answers one question: does this push publish a nested git
// repository that nobody declared?
//
// A directory that is itself a git repository is recorded by git as a GITLINK —
// a tree entry of mode 160000 holding a commit id and nothing else. `git add -A`
// records one for every such directory it finds, silently and in a single
// stroke, and the result looks like an ordinary file change in `git status`.
//
// It exists because that happened: an agent ran `git add -A` in a repository
// that held nineteen per-session worktrees under .claude/, and pushed nineteen
// gitlinks to a public repository in one commit. Nothing of the worktrees'
// CONTENT went up — a gitlink is only a commit id — but the paths did, the
// commit was wrong, and it had to be rewritten and force-pushed.
//
// The rule is structural rather than a list of directory names. A list would
// have to guess at .claude, .idea, node_modules, vendor, a checkout somebody
// made under /tmp inside a work tree — and would be blind to the twentieth. What
// is actually wrong is narrower and easier to state: a gitlink that no
// .gitmodules declares is not a submodule, it is an accident. A real submodule
// is always declared, because that is the only way a clone can restore it.
package gitlink

import (
	"bufio"
	"io"
	"strings"
)

// Mode is the tree entry mode git uses for a nested repository.
const Mode = "160000"

// Paths reads `git ls-tree -r <commit>` and returns the paths recorded as
// gitlinks, in the order they appear.
//
// Anything that is not a gitlink line is skipped rather than guessed at: a guard
// that refused a push because it misread a line would be worse than no guard.
func Paths(r io.Reader) []string {
	var out []string
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		line := s.Text()
		if !strings.HasPrefix(line, Mode+" ") {
			continue
		}
		// "160000 commit <sha>\t<path>" — the path is after the tab, and a path
		// may itself contain spaces, so the tab is the only safe separator.
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		if p := line[tab+1:]; p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Declared reads a .gitmodules file and returns the paths it declares.
//
// The format is git config syntax; only the `path =` lines matter here, and they
// are taken wherever they appear. Reading it loosely is deliberate: this decides
// whether to ALLOW a gitlink, so a line this fails to understand must not turn
// into a refusal.
func Declared(r io.Reader) map[string]bool {
	out := map[string]bool{}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "path" {
			continue
		}
		if v := strings.TrimSpace(value); v != "" {
			out[v] = true
		}
	}
	return out
}

// Undeclared returns the gitlinks this push would ADD without declaring them:
// present at the tip, absent at the base, named by no .gitmodules.
//
// Comparing against the base is what keeps the guard usable. A repository that
// has always carried an undeclared gitlink is not made worse by the next push,
// and a guard that refused every push in it would be turned off within the hour;
// this one only refuses the push that introduces one.
func Undeclared(tip, base []string, declared map[string]bool) []string {
	was := make(map[string]bool, len(base))
	for _, p := range base {
		was[p] = true
	}
	var out []string
	for _, p := range tip {
		if was[p] || declared[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}
