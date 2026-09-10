package gitlink

import (
	"strings"
	"testing"
)

// The line git actually writes. The path is after a TAB, and the mode/type/sha
// are separated by spaces — so a path containing a space is only parsed right if
// the tab is what splits it.
const lsTree = `100644 blob ce013625030ba8dba906f756967f9e9ca394464a	README.md
160000 commit 77f0cb3eb183cdea3b3b8d3b5d96ed7e6b88c8aa	.claude/worktrees/session-a
100755 blob 519dd581e50e5b45d3b3c76c3172e9c3ec293488	tools/run.sh
160000 commit 81b3c46df3c7f37877d4e4e026efd444238dce59	vendor/a name with spaces
`

func TestPathsReadsOnlyGitlinks(t *testing.T) {
	got := Paths(strings.NewReader(lsTree))
	want := []string{".claude/worktrees/session-a", "vendor/a name with spaces"}
	if len(got) != len(want) {
		t.Fatalf("Paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A guard that refused a push because it misread a line would be worse than no
// guard, so anything unrecognisable is skipped rather than guessed at.
func TestPathsSkipsWhatItCannotRead(t *testing.T) {
	for _, in := range []string{
		"",
		"nonsense\n",
		"160000 commit deadbeef\n",              // no tab, so no path
		"160000 commit deadbeef\t\n",            // a tab and nothing after it
		"1600001 commit deadbeef\tnot-a-mode\n", // a longer mode is a different mode
	} {
		if got := Paths(strings.NewReader(in)); len(got) != 0 {
			t.Errorf("Paths(%q) = %v, want none", in, got)
		}
	}
}

func TestDeclaredReadsGitmodules(t *testing.T) {
	got := Declared(strings.NewReader(`[submodule "vendor/sub"]
	path = vendor/sub
	url = https://example.invalid/sub.git
# a comment
; another
[submodule "second"]
  path   =   vendor/second
  branch = main
`))
	for _, want := range []string{"vendor/sub", "vendor/second"} {
		if !got[want] {
			t.Errorf("Declared lacks %q: %v", want, got)
		}
	}
	if got["main"] {
		t.Error("a branch = line was read as a path")
	}
	if len(got) != 2 {
		t.Errorf("Declared = %v, want two entries", got)
	}
}

// Reading .gitmodules loosely is deliberate: it decides whether to ALLOW a
// gitlink, so a line it cannot understand must not become a refusal.
func TestDeclaredIsEmptyRatherThanWrong(t *testing.T) {
	if got := Declared(strings.NewReader("[submodule \"x\"]\n\tpath =\n\tno equals here\n")); len(got) != 0 {
		t.Errorf("Declared = %v, want none", got)
	}
}

func TestUndeclaredIsWhatThisPushAdds(t *testing.T) {
	tip := []string{".claude/worktrees/a", "vendor/sub", "old/thing"}
	base := []string{"old/thing"}
	declared := map[string]bool{"vendor/sub": true}
	got := Undeclared(tip, base, declared)
	if len(got) != 1 || got[0] != ".claude/worktrees/a" {
		t.Errorf("Undeclared = %v, want [.claude/worktrees/a]", got)
	}
}

// A repository that has always carried an undeclared gitlink is not made worse
// by the next push. A guard that refused every push in it would be turned off
// within the hour, and then it would be guarding nothing.
func TestAGitlinkAlreadyThereIsNotRefusedAgain(t *testing.T) {
	same := []string{"legacy/checkout"}
	if got := Undeclared(same, same, nil); len(got) != 0 {
		t.Errorf("Undeclared = %v, want none", got)
	}
}

// A real submodule is declared, because that is the only way a clone can restore
// it — which is exactly what tells it apart from an accident.
func TestADeclaredSubmoduleGoesThrough(t *testing.T) {
	if got := Undeclared([]string{"vendor/sub"}, nil, map[string]bool{"vendor/sub": true}); len(got) != 0 {
		t.Errorf("Undeclared = %v, want none", got)
	}
}

func TestNoGitlinksAtAll(t *testing.T) {
	if got := Undeclared(nil, nil, nil); len(got) != 0 {
		t.Errorf("Undeclared = %v, want none", got)
	}
}
