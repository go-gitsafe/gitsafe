// gitpush pushes, including changes to .github/workflows, without a credential
// ever reaching a command line or an output stream.
//
// It is what I should have used instead of putting a token in a push URL. Three
// things make it safe, and none of them rely on remembering to be careful:
//
//  1. It NEVER reads the token. It only names a credential helper, so the secret
//     goes from a file to git over a pipe and this program never holds it.
//  2. It REFUSES to run if the remote URL carries a credential, rather than
//     helpfully using it. A URL with a secret in it is a mistake to be fixed,
//     not a convenience.
//  3. It redacts everything it prints, by SHAPE, so a secret arriving from
//     somewhere it was never told about — git's own message, the server's reply
//     — still cannot get out.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-gitsafe/gitsafe/emptypush"
	"github.com/go-gitsafe/gitsafe/helparg"
	"github.com/go-gitsafe/gitsafe/redact"
)

// helperName is the credential helper this asks git to use. It is looked up on
// PATH, the way git looks up `git-credential-*` helpers.
const helperName = "tokenfile"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 1 && helparg.Is(args[0]) {
		fmt.Fprintln(stdout, "usage: gitpush [git push arguments]")
		fmt.Fprintln(stdout, "pushes with a credential helper, so no token is ever on a command line")
		return 0
	}

	remote := remoteFrom(args)
	url, err := gitOutput("remote", "get-url", remote)
	if err != nil {
		fmt.Fprintf(stderr, "gitpush: cannot read the URL of remote %q: %v\n", remote, err)
		return 1
	}
	if !redact.Clean(url) {
		// Deliberately not printed: the whole point is that this string must not
		// reach a transcript.
		fmt.Fprintf(stderr, "gitpush: remote %q has a credential embedded in its URL.\n", remote)
		fmt.Fprintf(stderr, "  Fix it first:  git remote set-url %s https://github.com/<org>/<repo>.git\n", remote)
		return 1
	}

	// ⛔⛔ A PUSH THAT SENDS NOTHING REPORTS SUCCESS. Twice, thirteen days
	// apart, a branch was published holding none of its author's work — once
	// because a `;` ran this after a commit zsh had killed on a glob, once
	// because a PreToolUse hook refused the whole command line and the push
	// alone was re-issued afterwards. Both times git said `* [new branch]`
	// and exited 0, because publishing a ref that adds no objects is a
	// perfectly legitimate push.
	//
	// The refusal is here, not on the shape of the command. The first attempt
	// was a guard-bash rule against committing and pushing in one line;
	// guardscan measured it over the whole transcript record at 4137 runs and
	// 3378 distinct commands out of 594 557, against two occurrences of the
	// defect. That ratio replaces a workflow instead of protecting it. This
	// asks the one question that is true exactly when the harm is real.
	//
	// Every error here FAILS OPEN, the way the rest of this program does: a
	// check that cannot read the repository has no opinion, and must not
	// stand between someone and a push.
	if os.Getenv(emptypush.Escape) == "" {
		if branch, err := gitOutput("symbolic-ref", "--short", "HEAD"); err == nil && emptypush.Applies(args, branch) {
			// Every commit of HEAD already reachable from one of this
			// remote's refs means this push would add no commit at all.
			if n, err := gitOutput("rev-list", "--count", "HEAD", "--not", "--remotes="+remote); err == nil && n == "0" {
				fmt.Fprintf(stderr, "gitpush: refusing to push — %s.\n\n%s\n", emptypush.Why(remote), emptypush.Advice)
				return 1
			}
		}
	}

	full := append([]string{
		// An empty value CLEARS the helper list. Without it this only APPENDS,
		// and git asks the helpers in order — so an already-configured helper
		// answers first and this one is never consulted. That is not theory: the
		// first attempt was refused for want of a scope the other token lacks,
		// while this helper sat unused behind it.
		//
		// Both scopes are cleared, because a URL-scoped helper outranks a plain
		// one and github.com already has one configured.
		"-c", "credential.helper=",
		"-c", "credential.https://github.com.helper=",
		"-c", "credential.helper=" + helperName,
		// A prompt here would hang a non-interactive run forever, and answering
		// one is how a secret gets typed somewhere it is echoed.
		"push",
	}, args...)

	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()

	// Everything git said, with anything shaped like a credential taken out.
	r := redact.New()
	if s := strings.TrimRight(r.String(out.String()), "\n"); s != "" {
		fmt.Fprintln(stderr, s)
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errorsAs(runErr, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(stderr, "gitpush: %v\n", r.String(runErr.Error()))
		return 1
	}
	return 0
}

// remoteFrom picks the remote out of a push command line: the first argument
// that is not a flag. Anything else — a branch, a refspec, --tags — leaves the
// default in place.
func remoteFrom(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return "origin"
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(redact.New().String(errb.String())))
	}
	return strings.TrimSpace(out.String()), nil
}

// errorsAs is errors.As, kept local so the import list of a security tool stays
// as short as it can be.
func errorsAs(err error, target **exec.ExitError) bool {
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
