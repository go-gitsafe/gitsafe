package volatile

import "testing"

const (
	home   = "/Users/me"
	tmpdir = "/var/folders/xy/T/"
	scratch = "/private/tmp/claude-501/proj/session/scratchpad"
	durable = "/Users/me/Documents/VCS/GIT/localhost"
)

func TestRefuses(t *testing.T) {
	cases := []struct {
		name, cmd, cwd, rule, dir string
	}{
		{"clone into scratchpad", "git clone https://github.com/o/r " + scratch + "/r", durable, "clone", scratch + "/r"},
		{"clone default dir in scratchpad", "git clone https://github.com/o/r.git", scratch, "clone", scratch + "/r"},
		{"clone ssh form", "git clone git@github.com:o/r.git", "/tmp", "clone", "/tmp/r"},
		{"clone with valued flags", "git clone -b dev --depth 1 https://github.com/o/r /tmp/x", durable, "clone", "/tmp/x"},
		{"git -C volatile clone", "git -C /tmp clone https://github.com/o/r", durable, "clone", "/tmp/r"},
		{"gh repo clone", "gh repo clone o/r " + scratch + "/o-r", durable, "clone", scratch + "/o-r"},
		{"gh repo clone in cwd", "gh repo clone o/r -- --depth 1", scratch, "clone", scratch + "/r"},
		{"cd then clone", "cd " + scratch + " && gh repo clone o/r", durable, "clone", scratch + "/r"},
		{"init here", "git init", "/private/tmp/w", "init", "/private/tmp/w"},
		{"init path", "git init -q /tmp/new", durable, "init", "/tmp/new"},
		{"worktree add", "git worktree add -b topic /tmp/wt", durable, "worktree", "/tmp/wt"},
		{"commit in volatile cwd", "git commit -m x", scratch + "/r", "commit", scratch + "/r"},
		{"commit -C volatile", "git -C " + scratch + "/r commit -F msg", durable, "commit", scratch + "/r"},
		{"commit chained", "git add -A; git commit -m x", "/tmp/r", "commit", "/tmp/r"},
		{"tmpdir counts", "git clone https://x/r", "/var/folders/xy/T/a", "clone", "/var/folders/xy/T/a/r"},
		{"dev shm", "git init /dev/shm/r", durable, "init", "/dev/shm/r"},
		{"env prefix that is not the escape", "GIT_TRACE=1 git commit -m x", "/tmp/r", "commit", "/tmp/r"},
		{"escape set to 0 is no escape", Escape + "=0 git init", "/tmp/r", "init", "/tmp/r"},
		{"relative climb into tmp", "git clone u ../../tmp/r", "/Users/me", "clone", "/tmp/r"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := Check(c.cmd, c.cwd, home, tmpdir)
			if !f.Found() {
				t.Fatalf("Check(%q) in %s: not refused", c.cmd, c.cwd)
			}
			if f.Rule != c.rule || f.Dir != c.dir {
				t.Errorf("got rule %q dir %q, want %q %q", f.Rule, f.Dir, c.rule, c.dir)
			}
			if f.Match == "" || f.Why == "" {
				t.Errorf("finding without match or reason: %+v", f)
			}
		})
	}
}

func TestAllows(t *testing.T) {
	cases := []struct{ name, cmd, cwd string }{
		{"clone durable", "gh repo clone o/r o-r", durable},
		{"clone durable from scratchpad cwd", "git clone https://github.com/o/r " + durable + "/r", scratch},
		{"clone home tilde", "git clone u ~/src/r", scratch},
		{"clone $HOME", "git clone u $HOME/src/r", scratch},
		{"cd durable then clone", "cd " + durable + " && gh repo clone o/r", scratch},
		{"cd home then init", "cd && git init r", "/tmp"},
		{"var tmp survives", "git init /var/tmp/r", durable},
		{"commit durable", "git commit -m x", durable + "/r"},
		{"commit -C durable from tmp", "git -C " + durable + "/r commit -m x", "/tmp"},
		{"log to scratchpad", "go test ./... > " + scratch + "/test.log", durable},
		{"build output to scratchpad", "go build -o " + scratch + "/bin ./cmd/x", durable},
		{"read-only git in tmp", "git status", "/tmp/r"},
		{"git log in tmp", "git -C /tmp/r log --oneline", durable},
		{"worktree list", "git worktree list", "/tmp/r"},
		{"worktree add durable", "git worktree add ../wt", durable + "/r"},
		{"escape", Escape + "=1 git init", "/tmp/x"},
		{"escape on clone", Escape + "=yes gh repo clone o/r /tmp/r", durable},
		{"clone without arguments", "git clone", "/tmp"},
		{"gh repo view", "gh repo view o/r", "/tmp"},
		{"quoted heredoc text", "cat <<'EOF'\ngit init /tmp/x\nEOF", durable},
		{"tmp-like name", "git init /tmpfoo/r", durable},
		{"empty", "", "/tmp"},
		{"only assignments", "A=1 B=2", "/tmp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if f := Check(c.cmd, c.cwd, home, tmpdir); f.Found() {
				t.Errorf("Check(%q) in %s refused: %+v", c.cmd, c.cwd, f)
			}
		})
	}
}

func TestVolatile(t *testing.T) {
	for dir, want := range map[string]bool{
		"/tmp": true, "/tmp/": true, "/private/tmp/a": true, "/var/folders/a": true,
		"/run/user/501/x": true, "/var/tmp": false, "/Users/me": false, "/tmpx": false,
		"/var/folders/xy/T": true,
	} {
		if got := Volatile(dir, ""); got != want {
			t.Errorf("Volatile(%q) = %v, want %v", dir, got, want)
		}
	}
	if !Volatile("/opt/t/x", "/opt/t") {
		t.Error("$TMPDIR is not counted")
	}
	if Volatile("/opt/t/x", "") {
		t.Error("an unset $TMPDIR counts as /")
	}
}

func FuzzCheck(f *testing.F) {
	for _, s := range []string{"git clone u /tmp/r", "cd /tmp && gh repo clone o/r", "git -C", "git worktree add", "cd", Escape + "="} {
		f.Add(s, "/tmp")
	}
	f.Fuzz(func(t *testing.T, cmd, cwd string) {
		fd := Check(cmd, cwd, home, tmpdir)
		if fd.Found() && !Volatile(fd.Dir, tmpdir) {
			t.Fatalf("refused %q for a durable directory %q", cmd, fd.Dir)
		}
	})
}
