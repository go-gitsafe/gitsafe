// git-post-checkout-guard strips a credential out of a remote URL at the moment
// a clone writes one, and says so loudly.
//
// It is installed as a GLOBAL git hook, so it applies to every repository and
// every tool that runs git on this machine.
//
// # Why here, of all places
//
// A token sat in the fetch and push URL of 117 checkouts here for three months.
// The existing pre-push guard refuses a PUSH from a credentialed remote, and
// that is where it was caught the first two times — but a push is not where the
// URL is written. `git clone https://TOKEN@host/o/r` writes it into
// .git/config, prints it on that first fetch, and prints it again on every
// fetch afterwards. Between the clone and the first push there may be months,
// and in this incident there were three.
//
// There is no pre-clone hook and no hook on a config write, so the earliest
// moment a hook can run at all is post-checkout — which git runs at the end of
// a clone. The clone has already happened by then and the token has already
// been printed once. What this can still do is take the credential out of the
// file before the NEXT fetch echoes it, and tell the person their token must be
// treated as disclosed while they are still looking at the terminal.
//
// # What it does not cover
//
// Being explicit about this is the point of it existing: a guard whose limits
// are unstated is how the incident lasted three months.
//
//   - `git clone --bare`, `--mirror` and `--no-checkout` run NO post-checkout
//     hook. Nothing catches those.
//   - `git fetch https://TOKEN@host/…` and `git pull <url>` write nothing to
//     config, so no hook runs, and git may still echo the URL. Not covered.
//   - `git remote add` / `git remote set-url` with a credential: git has no
//     hook on a configuration write. Not covered until the next scan.
//   - A repository that sets its own core.hooksPath replaces the global one, so
//     this stops applying there.
//   - It runs on every checkout, not only a clone, and only reads the local
//     configuration: a credential in the global config or in ~/.git-credentials
//     is somebody else's problem.
//   - And it is remediation, not prevention: by the time it runs, the
//     credential has been on a command line and in git's output. It must still
//     be revoked.
//
// The barrier that actually holds for the cases above is `credscan scan` on a
// schedule, which exits non-zero, plus the pre-push refusal that was already
// here.
//
// # Installing it
//
//	go build -o ~/.config/git/hooks/post-checkout ./cmd/git-post-checkout-guard
//	git config --global core.hooksPath ~/.config/git/hooks
//
// git invokes it as `post-checkout <old> <new> <branch-flag>` and ignores its
// output only when it exits 0; a non-zero exit makes the surrounding command
// report a failed hook, which is what makes the message hard to miss.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-gitsafe/gitsafe/credfix"
)

// repair is the action, as a variable so a test can watch what the hook decided
// without needing a repository for every case.
var repair = credfix.Repair

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	// The escape is deliberate and loud rather than absent. There are
	// repositories whose remote legitimately carries a credential nobody can
	// change today — a CI runner's own checkout, a mirror behind a gateway —
	// and a guard with no way out is a guard that gets uninstalled.
	if os.Getenv("GITSAFE_ALLOW_URL_CREDENTIAL") != "" {
		return chain(args, stdout, stderr)
	}

	r := repair(".", true)
	if !r.Readable() {
		// No opinion rather than a refusal: an unreadable repository here means
		// this hook could not do its job, not that a checkout should fail.
		// `credscan scan` is what counts those, and it counts them as
		// unreadable rather than as clean.
		fmt.Fprintf(stderr, "git-post-checkout-guard: cannot read this repository's configuration: %v\n", r.Err)
		return chain(args, stdout, stderr)
	}
	if r.Before() == 0 {
		return chain(args, stdout, stderr)
	}

	// Never the value: reporting a secret to complain about it is the mistake
	// itself.
	fmt.Fprintf(stderr, "\ngit: this checkout's remote URL carried a CREDENTIAL, and git has printed it.\n\n")
	for _, e := range r.Leaks {
		fmt.Fprintf(stderr, "  %s\n      %s\n", e.Key, e.Finding)
		switch {
		case e.Repaired:
			fmt.Fprintf(stderr, "      taken out of the configuration, and read back: now %s\n", e.Clean)
		default:
			fmt.Fprintf(stderr, "      STILL THERE — this one needs you: credscan clean %s --write\n", quote(cwd()))
		}
	}
	fmt.Fprintf(stderr, "\n  A token in a URL is echoed by git, lands in .git/config, and is printed again\n")
	fmt.Fprintf(stderr, "  by every fetch from now on. One sat in 117 checkouts here for three months.\n")
	fmt.Fprintf(stderr, "  Treat it as DISCLOSED: revoke it and issue a new one.\n\n")
	fmt.Fprintf(stderr, "  Fetches from this repository now use your credential helper, which is how it\n")
	fmt.Fprintf(stderr, "  should have been cloned:\n")
	fmt.Fprintf(stderr, "      git clone https://github.com/<org>/<repo>.git\n")
	fmt.Fprintf(stderr, "      gitpush origin <branch>\n\n")
	fmt.Fprintf(stderr, "  This hook cannot stop a clone — git has no hook before one — so the clone you\n")
	fmt.Fprintf(stderr, "  just ran did happen. Run `credscan scan` to see whether others are carrying\n")
	fmt.Fprintf(stderr, "  the same one.\n\n")

	// Non-zero, so that the command that ran the hook reports a failure rather
	// than scrolling this away. The checkout itself stands: git has already
	// done it, and pretending otherwise would be a lie about the state of the
	// disk.
	_ = chain(args, stdout, stderr)
	return 1
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t'\"") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// chain runs the repository's own post-checkout hook, if it has one.
//
// Setting core.hooksPath globally makes git look ONLY there, which silently
// disables any hook a repository installs for itself. Nothing on this machine
// has one today, and a guard that quietly broke a future one would be a poor
// trade.
func chain(args []string, stdout, stderr io.Writer) int {
	dir, err := gitOutput("rev-parse", "--git-dir")
	if err != nil {
		return 0
	}
	local := filepath.Join(dir, "hooks", "post-checkout")
	info, err := os.Stat(local)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return 0
	}
	cmd := exec.Command(local, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(stderr, "git-post-checkout-guard: %s: %v\n", local, err)
		return 1
	}
	return 0
}

func gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func asExit(err error, target **exec.ExitError) bool {
	for err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			*target = ee
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
