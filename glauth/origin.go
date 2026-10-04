package glauth

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/go-gitsafe/gitsafe/credurl"
)

// Origin returns the GitLab project the repository in dir pushes to, read from
// its origin remote.
//
// A remote URL that carries a credential is refused and never repeated: that
// URL is a leak waiting for the next fetch to echo it, and must be fixed rather
// than used.
func Origin(dir string) (Remote, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return Remote{}, errors.New("no origin remote here (run inside a clone, or pass the project explicitly)")
	}
	raw := strings.TrimSpace(string(out))
	if credurl.Inspect(raw).Leak() {
		return Remote{}, errors.New("the origin remote URL carries a credential: remove it (credscan finds and strips it) before using this repository")
	}
	r, ok := ParseRemote(raw)
	if !ok {
		return Remote{}, errors.New("the origin remote is not a GitLab project URL this understands")
	}
	return r, nil
}

// CurrentBranch is the branch checked out in dir, or "" when HEAD is detached.
func CurrentBranch(dir string) string {
	cmd := exec.Command("git", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
