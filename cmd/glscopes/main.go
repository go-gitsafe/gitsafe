// glscopes is ghscopes for GitLab: it says which account a token belongs to,
// what it may do and until when, without ever showing the token.
//
//	glscopes                       the token for this clone's GitLab instance
//	glscopes read_api              the same, and fail unless it may do that
//	glscopes -host gitlab.com api
//	glscopes -f ~/.gitlab-token-plmlab.math.cnrs.fr
//
// The first look at a GitLab token on this machine printed it: a format check
// expected "PRIVATE-TOKEN: <token>" in the file, found the bare token, and
// printed its "header name". The answer to "is this token right?" is what the
// instance says about it, asked with the token in a header — never something
// read out of the file.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/glauth"
)

var osExit = os.Exit

// now is a variable so a test can fix the clock the expiry is counted from.
var now = time.Now

// newClient is a variable so a test can point the tool at a fake instance.
var newClient = glauth.New

func main() { osExit(run(os.Args[1:], ".", os.Stdout, os.Stderr)) }

func run(args []string, dir string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("glscopes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", "", "GitLab instance (default: this clone's origin, else gitlab.com)")
	file := fs.String("f", "", "token file (default: ~/.gitlab-token-<host>, else ~/.gitlab-token)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	want := fs.Args()

	if *host == "" {
		if r, err := glauth.Origin(dir); err == nil {
			*host = r.Host
		} else {
			*host = "gitlab.com"
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "glscopes: no home directory: %v\n", err)
		return 1
	}
	path := glauth.TokenPath(home, *host, *file)
	tok, err := glauth.Read(path)
	if err != nil {
		fmt.Fprintf(stderr, "glscopes: %v\n", err)
		return 1
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(stderr, "glscopes: warning: %s is readable by others (%v); chmod 600 it\n", path, fi.Mode().Perm())
	}

	c := newClient(*host, tok)
	self, err := c.Self()
	if err != nil {
		var r *glauth.Refusal
		if errors.As(err, &r) && r.Status == 404 {
			fmt.Fprintf(stderr, "glscopes: %s does not answer /personal_access_tokens/self: is it a GitLab instance, and is this a personal access token?\n", *host)
			return 1
		}
		fmt.Fprintf(stderr, "glscopes: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "file:     %s\n", path)
	fmt.Fprintf(stdout, "instance: %s\n", *host)
	if u := c.Username(); u != "" {
		fmt.Fprintf(stdout, "account:  %s\n", u)
	}
	fmt.Fprintf(stdout, "token:    %s\n", self.Name)
	fmt.Fprintf(stdout, "scopes:   %s\n", strings.Join(self.Scopes, ", "))
	fmt.Fprintf(stdout, "expires:  %s\n", expiry(self.ExpiresAt))
	if !self.Active || self.Revoked {
		fmt.Fprintf(stderr, "glscopes: the token is not active (revoked=%v)\n", self.Revoked)
		return 1
	}
	if missing := glauth.Missing(self.Scopes, want); len(missing) > 0 {
		fmt.Fprintf(stderr, "glscopes: missing scope(s): %s\n", strings.Join(missing, ", "))
		return 1
	}
	if len(want) > 0 {
		fmt.Fprintf(stdout, "has:      %s\n", strings.Join(want, ", "))
	}
	return 0
}

// expiry says how long is left, because a date alone does not say "tomorrow".
func expiry(date string) string {
	if date == "" {
		return "never"
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	days := int(t.Sub(now().Truncate(24*time.Hour)).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf("%s (expired)", date)
	case days == 0:
		return fmt.Sprintf("%s (today)", date)
	}
	return fmt.Sprintf("%s (in %d days)", date, days)
}
