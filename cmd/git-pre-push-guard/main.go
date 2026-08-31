// git-pre-push-guard refuses any push to a URL that carries a credential.
//
// It is installed as a GLOBAL git hook, so it applies to every repository, every
// tool and every person or agent that runs git on this machine — not only to
// those that remembered to use the safe wrapper. Documentation asks; a hook
// refuses.
//
// It exists because a token was written into a push URL twice and git echoed it
// back both times. The wrapper that prevents that is only used by whoever knows
// to use it; this is the floor underneath.
//
// git invokes a pre-push hook as `pre-push <remote-name> <remote-url>` and
// passes the refs on stdin. Exiting non-zero aborts the push.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tannevaled/gitsafe/protect"
	"github.com/tannevaled/gitsafe/redact"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// A URL is the second argument. With fewer, there is nothing to judge and
	// the push is none of this hook's business.
	if len(args) >= 2 && !redact.Clean(args[1]) {
		remote := args[0]
		// The offending URL is deliberately NOT printed: repeating a secret to
		// complain about it is the mistake itself.
		fmt.Fprintf(stderr, "\ngit: refusing to push — the URL for remote %q carries a credential.\n\n", remote)
		fmt.Fprintf(stderr, "  A token in a URL is echoed by git, lands in .git/config, and ends up in\n")
		fmt.Fprintf(stderr, "  transcripts and scrollback. It has happened here before.\n\n")
		fmt.Fprintf(stderr, "  Fix the remote, then push again:\n")
		fmt.Fprintf(stderr, "      git remote set-url %s https://github.com/<org>/<repo>.git\n", remote)
		fmt.Fprintf(stderr, "      gitpush %s <branch>\n\n", remote)
		fmt.Fprintf(stderr, "  gitpush uses a credential helper, so no secret is ever on a command line.\n\n")
		return 1
	}
	// The refs are needed both to judge and to chain, so they are read once and
	// replayed. A hook that swallowed its own stdin would leave a repository's
	// own hook reading nothing and deciding on it.
	refs, _ := io.ReadAll(stdin)
	if code := judgeBranches(args, refs, stderr); code != 0 {
		return code
	}
	return chain(args, bytes.NewReader(refs), stdout, stderr)
}

// judgeBranches refuses a write to the branch pull requests land on.
//
// It is here, in the global hook, and not only in the wrapper: the wrapper is
// what a person or an agent remembers to use, and this is what happens whatever
// they run. The rule was asked for after a fix went straight onto main by
// habit -- green, tested and reviewed by nobody.
//
// The escape is deliberate and loud rather than absent, because there are
// pushes that legitimately have nowhere else to go: an unattended repair to a
// branch protection nobody can review, a repository being bootstrapped. Setting
// GITSAFE_ALLOW_DEFAULT_BRANCH=1 for one command is an act, not an oversight,
// and it says in the transcript what was done.
func judgeBranches(args []string, refs []byte, stderr io.Writer) int {
	if os.Getenv("GITSAFE_ALLOW_DEFAULT_BRANCH") != "" {
		return 0
	}
	remote := "origin"
	if len(args) >= 1 && args[0] != "" {
		remote = args[0]
	}
	blocked := protect.Blocked(protect.Parse(bytes.NewReader(refs)), remoteDefault(remote))
	if len(blocked) == 0 {
		return 0
	}
	fmt.Fprintf(stderr, "\ngit: refusing to write %s on %q — that is where pull requests land.\n\n",
		strings.Join(blocked, ", "), remote)
	fmt.Fprintf(stderr, "  Work goes onto a branch and lands through a pull request, so that the\n")
	fmt.Fprintf(stderr, "  checks run against it and somebody can read it before it is the\n")
	fmt.Fprintf(stderr, "  default branch. A change pushed straight here has neither.\n\n")
	fmt.Fprintf(stderr, "  Instead:\n")
	fmt.Fprintf(stderr, "      git switch -c <a-name-that-says-what-this-does>\n")
	fmt.Fprintf(stderr, "      gitpush -u %s <that-branch>\n", remote)
	fmt.Fprintf(stderr, "      gh pr create   # and merge it once the checks are green\n\n")
	fmt.Fprintf(stderr, "  Tags are not affected: a release tag goes straight up as always.\n")
	fmt.Fprintf(stderr, "  If this really has to go on the default branch, say so on purpose:\n")
	fmt.Fprintf(stderr, "      GITSAFE_ALLOW_DEFAULT_BRANCH=1 gitpush %s ...\n\n", remote)
	return 1
}

// remoteDefault asks the remote what its default branch is called, and answers
// "" when it will not say.
//
// Not fatal when it fails: main and master are protected anyway, and a guard
// that needed the network to work would be a guard that stops working on a
// train.
func remoteDefault(remote string) string {
	ref, err := gitOutput("symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(ref, remote+"/")
}

// chain runs the repository's own pre-push hook, if it has one.
//
// Setting core.hooksPath globally makes git look ONLY there, which would
// silently disable any hook a repository installs for itself. Nothing on this
// machine has one today, and a guard that quietly breaks a future one would be a
// poor trade for the protection it gives.
func chain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	dir, err := gitOutput("rev-parse", "--git-dir")
	if err != nil {
		return 0 // not a repository, or git cannot say: nothing to chain to
	}
	local := filepath.Join(dir, "hooks", "pre-push")
	info, err := os.Stat(local)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return 0
	}
	cmd := exec.Command(local, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if ok := asExit(err, &ee); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(stderr, "git-pre-push-guard: %s: %v\n", local, err)
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
