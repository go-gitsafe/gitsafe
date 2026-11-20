package plain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ⛔ THE SNAPSHOT THE WRAPPER SOURCES (#156). The Bash tool runs `source <snapshot>`
// before every command, and the snapshot can change how the NEXT text parses: a
// global alias (`alias -g`) or a suffix alias replaces a word anywhere, and an
// option such as extendedglob, interactivecomments, rcquotes or ignorebraces
// moves the very grammar S+ was measured against. S+ was measured with an empty
// snapshot, so this test reads the real one and refuses to pass over a change that
// could make the measurement stale. A regular alias only renames a command word and
// leaves the quoting around its arguments alone; it is reported, not failed, unless
// its value holds a character that opens a substitution.

// snapshotRisks lists what a snapshot does that the oracle's empty snapshot does not.
func snapshotRisks(text string) []string {
	var out []string
	// options a snapshot may set without moving how the grammar parses
	harmless := map[string]bool{"nohashdirs": true, "hashdirs": true, "login": true, "nologin": true}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "alias "):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "alias "))
			if strings.HasPrefix(rest, "-g") || strings.HasPrefix(rest, "-s") {
				out = append(out, "global or suffix alias: "+line)
				continue
			}
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "--"))
			if _, v, ok := strings.Cut(rest, "="); ok && strings.ContainsAny(v, "`$;&|<>()\n") {
				out = append(out, "alias whose value can open a substitution or a list: "+line)
			}
		case strings.HasPrefix(line, "unsetopt "):
			out = append(out, "unsetopt may move the grammar: "+line)
		case strings.HasPrefix(line, "setopt "):
			for _, o := range strings.Fields(line)[1:] {
				if !harmless[strings.ToLower(strings.ReplaceAll(o, "_", ""))] {
					out = append(out, "option that may move the grammar: "+line)
				}
			}
		case strings.HasPrefix(line, "emulate ") || strings.HasPrefix(line, "set -o ") || strings.HasPrefix(line, "set +o "):
			out = append(out, "shell mode change: "+line)
		}
	}
	sort.Strings(out)
	return out
}

func TestSnapshotRisksSeesWhatItIsFor(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		risks      int
	}{
		{"a clean snapshot", "unalias -a\nalias -- run-help=man\nalias -- which-command=whence\nsetopt nohashdirs\nsetopt login\n", 0},
		{"a global alias", "alias -g L='| less'\n", 1},
		{"a suffix alias", "alias -s go=vim\n", 1},
		{"an alias that substitutes", "alias d='echo `date`'\n", 1},
		{"an alias with a list", "alias gs='git status; ls'\n", 1},
		{"an option that moves the grammar", "setopt extended_glob\n", 1},
		{"interactivecomments", "setopt interactive_comments\n", 1},
		{"unsetopt of anything", "unsetopt bareglobqual\n", 1},
		{"emulate", "emulate sh\n", 1},
	} {
		if got := snapshotRisks(tc.text); len(got) != tc.risks {
			t.Errorf("%s: %d risk(s) %v, want %d", tc.name, len(got), got, tc.risks)
		}
	}
}

// The real snapshots of this machine (the newest few), when they exist. A machine
// with none (CI) says so rather than pass silently.
func TestTheLiveSnapshotsAddNothingToTheGrammar(t *testing.T) {
	dir := os.Getenv("GITSAFE_SNAPSHOT_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude", "shell-snapshots")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "snapshot-zsh-*.sh"))
	if len(files) == 0 {
		t.Skipf("no shell snapshot under %s: the alias half of the oracle was not asked", dir)
	}
	sort.Strings(files)
	if len(files) > 20 {
		files = files[len(files)-20:]
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if risks := snapshotRisks(string(b)); len(risks) > 0 {
			t.Errorf("%s changes how a command parses, so S+'s measurement with an empty snapshot is stale:\n  %s",
				filepath.Base(f), strings.Join(risks, "\n  "))
		}
	}
}
