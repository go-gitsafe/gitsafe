// Package protect answers one question: does this push write the branch a
// repository's pull requests are supposed to land on?
//
// It is a package rather than a function in one command because two things ask
// it -- the wrapper, so the refusal is quick and legible, and the global hook,
// so it is refused whoever runs git and whatever they run it with. The hook is
// the floor; the wrapper is the manners.
package protect

import (
	"bufio"
	"io"
	"strings"
)

// Names are the branch names protected wherever a repository does not say
// otherwise. A repository whose default branch is something else is covered by
// the remote's own HEAD, which [Refs] takes as well.
var Names = []string{"main", "master"}

// zeroSHA is what git writes for a ref that does not exist yet, or that is
// being deleted.
const zeroSHA = "0000000000000000000000000000000000000000"

// Update is one line of what git feeds a pre-push hook on stdin.
type Update struct {
	LocalRef  string
	LocalSHA  string
	RemoteRef string
	RemoteSHA string
}

// Creating reports whether this update makes a ref that is not there yet.
//
// It is allowed even for a protected name: a repository whose main does not
// exist has nothing to open a pull request against, which is the shape of every
// first push to a new repository.
func (u Update) Creating() bool { return u.RemoteSHA == zeroSHA }

// Parse reads the ref updates git writes to a pre-push hook's stdin.
//
// Anything that is not four fields is skipped rather than guessed at: a hook
// that refused a push because it misread a line would be worse than no hook.
func Parse(r io.Reader) []Update {
	var out []Update
	s := bufio.NewScanner(r)
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) != 4 {
			continue
		}
		out = append(out, Update{LocalRef: f[0], LocalSHA: f[1], RemoteRef: f[2], RemoteSHA: f[3]})
	}
	return out
}

// Branch returns the branch a ref names, or "" when it is not a branch.
//
// TAGS ARE NOT BRANCHES, and this is the line that says so: a release tag is
// pushed to a repository whose default branch is protected, all day long, and a
// guard that refused those would be turned off within the hour.
func Branch(ref string) string {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.TrimPrefix(ref, prefix)
}

// Protected reports whether name is a branch this refuses to write, given the
// remote's own default branch (which may be empty when it is not known).
func Protected(name, remoteDefault string) bool {
	if name == "" {
		return false
	}
	if remoteDefault != "" && name == remoteDefault {
		return true
	}
	for _, n := range Names {
		if name == n {
			return true
		}
	}
	return false
}

// Blocked returns the protected branches an update list would write, in the
// order they appear, ignoring creations and everything that is not a branch.
func Blocked(ups []Update, remoteDefault string) []string {
	var out []string
	for _, u := range ups {
		b := Branch(u.RemoteRef)
		if !Protected(b, remoteDefault) || u.Creating() {
			continue
		}
		out = append(out, b)
	}
	return out
}
