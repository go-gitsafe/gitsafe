// Package volatile answers one question: would this command put a git working
// tree — a clone, a new repository, a worktree, or a commit — in a directory
// that does not survive a reboot?
//
// It exists because the written rule failed five times. The session
// scratchpad lives under /private/tmp, which macOS purges, and every time work
// was lost the same way: a clone made there, commits made there, nothing pushed
// yet. On 2026-10-28 a branch holding a dispatch package, a correctness fix and
// a 9 000-call golden file vanished with the scratchpad; it was rebuilt only
// because the session that wrote it was still alive.
//
// # What it refuses
//
// The act is "the only copy of work lives in a directory that will be
// emptied", stated through the commands that create that only copy:
//
//	git clone <url> [dir]            # into a volatile directory
//	gh repo clone <repo> [dir]
//	git init [dir]
//	git worktree add <path>
//	git commit                       # in a working tree under a volatile directory
//
// # What it does NOT refuse
//
// Reading and writing ephemeral files there is what the scratchpad is for: a
// log redirected to a file, a build output, a downloaded archive inspected
// once. Those are allowed — the rule is about repositories, which hold work
// that is not anywhere else until it is pushed.
package volatile

import (
	"path"
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	Rule  string
	Match string
	Why   string
	// Dir is the volatile directory the work would land in.
	Dir string
}

// Found reports whether anything was found.
func (f Finding) Found() bool { return f.Rule != "" }

// Escape is the assignment that says a volatile working tree is meant, as a
// prefix on the command (the guard reads the command as text, see
// discard.Escape for why a prefix and not an environment variable).
const Escape = "GITSAFE_ALLOW_VOLATILE"

// roots are the directories this machine empties: /tmp and its macOS
// spelling, the per-user temporary tree macOS gives $TMPDIR, and Linux's
// memory-backed ones. /var/tmp is not among them: it survives a reboot.
var roots = []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders", "/dev/shm", "/run/user"}

// Volatile reports whether dir lies under a directory that does not survive a
// reboot; tmpdir is $TMPDIR, which counts too when set.
func Volatile(dir, tmpdir string) bool {
	dir = path.Clean(dir)
	rs := roots
	if tmpdir != "" {
		rs = append(rs[:len(rs):len(rs)], path.Clean(tmpdir))
	}
	for _, r := range rs {
		if dir == r || strings.HasPrefix(dir, r+"/") {
			return true
		}
	}
	return false
}

// Check reports whether cmd, run in cwd, would make or commit to a working tree
// under a volatile directory. home and tmpdir are $HOME and $TMPDIR. A `cd` in
// the command moves the directory the later commands are judged in.
func Check(cmd, cwd, home, tmpdir string) Finding {
	dir := cwd
	for _, part := range segments(shellcmd.StripQuotedHeredocs(cmd)) {
		fields := strings.Fields(part)
		i := 0
		for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "-") {
			if name, value, _ := strings.Cut(fields[i], "="); name == Escape && value != "" && value != "0" {
				return Finding{}
			}
			i++
		}
		if i >= len(fields) {
			continue
		}
		name, args := base(fields[i]), fields[i+1:]
		if name == "cd" {
			if len(args) == 0 {
				dir = home
			} else {
				dir = resolve(dir, home, args[0])
			}
			continue
		}
		if f := checkOne(name, args, dir, home, tmpdir); f.Found() {
			return f
		}
	}
	return Finding{}
}

func checkOne(name string, args []string, dir, home, tmpdir string) Finding {
	switch name {
	case "git":
		at, sub, rest := gitSubcommand(args, dir, home)
		switch sub {
		case "clone":
			if dest := cloneDest(rest, at, home, true); dest != "" && Volatile(dest, tmpdir) {
				return Finding{"clone", "git clone", "a clone in a volatile directory is the only copy of whatever is committed there before a push", dest}
			}
		case "init":
			dest := at
			if ps := positionals(rest, nil); len(ps) > 0 {
				dest = resolve(at, home, ps[0])
			}
			if Volatile(dest, tmpdir) {
				return Finding{"init", "git init", "a repository started in a volatile directory is lost with it", dest}
			}
		case "worktree":
			if len(rest) > 0 && rest[0] == "add" {
				if ps := positionals(rest[1:], map[string]bool{"-b": true, "-B": true, "--reason": true}); len(ps) > 0 {
					if dest := resolve(at, home, ps[0]); Volatile(dest, tmpdir) {
						return Finding{"worktree", "git worktree add", "a worktree in a volatile directory loses its uncommitted and unpushed work", dest}
					}
				}
			}
		case "commit":
			if Volatile(at, tmpdir) {
				return Finding{"commit", "git commit", "a commit in a working tree under a volatile directory exists nowhere else until it is pushed", at}
			}
		}
	case "gh":
		if len(args) >= 2 && args[0] == "repo" && args[1] == "clone" {
			if dest := cloneDest(args[2:], dir, home, false); dest != "" && Volatile(dest, tmpdir) {
				return Finding{"clone", "gh repo clone", "a clone in a volatile directory is the only copy of whatever is committed there before a push", dest}
			}
		}
	}
	return Finding{}
}

// gitSubcommand returns the directory git works in (cwd, moved by -C), its
// subcommand and the arguments after it.
func gitSubcommand(args []string, dir, home string) (string, string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" && i+1 < len(args):
			dir = resolve(dir, home, args[i+1])
			i++
		case a == "-c" || a == "--git-dir" || a == "--work-tree":
			i++
		case !strings.HasPrefix(a, "-"):
			return dir, a, args[i+1:]
		}
	}
	return dir, "", nil
}

// cloneValued are the clone options of git and gh that take a value.
var cloneValued = map[string]bool{
	"-b": true, "--branch": true, "-o": true, "--origin": true, "--depth": true, "-u": true,
	"--upload-pack": true, "--reference": true, "--separate-git-dir": true, "-c": true, "--config": true,
	"--template": true, "-j": true, "--jobs": true, "--filter": true, "--upstream-remote-name": true,
}

// cloneDest is where a clone lands: the second positional argument, or the
// repository's base name in dir. gh's arguments after `--` are git's flags.
func cloneDest(args []string, dir, home string, isGit bool) string {
	if !isGit {
		for i, a := range args {
			if a == "--" {
				args = args[:i]
				break
			}
		}
	}
	ps := positionals(args, cloneValued)
	switch len(ps) {
	case 0:
		return ""
	case 1:
		name := strings.TrimSuffix(path.Base(strings.TrimRight(ps[0], "/")), ".git")
		if i := strings.LastIndexByte(name, ':'); i >= 0 {
			name = name[i+1:]
		}
		return resolve(dir, home, name)
	}
	return resolve(dir, home, ps[1])
}

// positionals returns the arguments that are not options or option values.
func positionals(args []string, valued map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i+1:]...)
		}
		if strings.HasPrefix(a, "-") {
			if valued[a] && !strings.Contains(a, "=") {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// resolve makes p absolute against dir, expanding a leading ~ or $HOME.
func resolve(dir, home, p string) string {
	p = strings.Trim(p, `"'`)
	switch {
	case p == "~" || p == "$HOME":
		return home
	case strings.HasPrefix(p, "~/"):
		return path.Join(home, p[2:])
	case strings.HasPrefix(p, "$HOME/"):
		return path.Join(home, p[6:])
	case strings.HasPrefix(p, "/"):
		return path.Clean(p)
	}
	return path.Join(dir, p)
}

func segments(cmd string) []string {
	f := func(r rune) bool { return r == ';' || r == '&' || r == '|' || r == '\n' }
	return strings.FieldsFunc(cmd, f)
}

func base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Advice is what to do instead, printed with a refusal.
const Advice = `Work that must survive goes in a durable directory, never in /tmp, the session
scratchpad or $TMPDIR, which this machine empties:

    cd ~/Documents/VCS/GIT/localhost && gh repo clone <org>/<repo> <org>-<repo>[-<topic>]

and push a new branch on its first commit (gitpush -u origin <branch>). The
scratchpad stays fine for one-turn files: a redirected log, a build output.

If a throwaway repository there is really meant, say so for one command:

    GITSAFE_ALLOW_VOLATILE=1 <the command>`
