package discard

import "testing"

// `git restore` writes the working tree with --worktree or -W too, past a `--`
// and past other options.
func TestRestoreWorktreeSpellings(t *testing.T) {
	for _, cmd := range []string{
		"git restore --worktree f",
		"git restore -W f",
		"git restore --staged --worktree f",
		"git restore -W -- f",
		"git restore --source=HEAD~1 -- f",
	} {
		if !Check(cmd).Found() {
			t.Errorf("%q was not refused", cmd)
		}
	}
	for _, cmd := range []string{
		"git restore --staged f",
		"git restore -S -- f",
		"git restore --worktree",
		"git restore --",
	} {
		if Check(cmd).Found() {
			t.Errorf("%q was refused", cmd)
		}
	}
}

// A git with only options, or nothing, has no subcommand to judge.
func TestGitWithNoSubcommandIsAllowed(t *testing.T) {
	for _, cmd := range []string{"git", "git -C /r", "git -c k=v --git-dir /g"} {
		if Check(cmd).Found() {
			t.Errorf("%q was refused", cmd)
		}
	}
	if sub, rest := subcommand([]string{"-C", "/r"}); sub != "" || rest != nil {
		t.Errorf("subcommand = %q, %q", sub, rest)
	}
}
