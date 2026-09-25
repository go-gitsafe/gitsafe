// Command ghpkg lists and deletes the versions of a published package.
//
//	ghpkg list   <org> <package>
//	ghpkg delete <org> <package> <tag> [--yes]
//
// # Why this exists
//
// A repair sweep published a version nobody wanted — a pre-release that became
// the newest of its major, moving every consumer pinned to that major onto a
// beta. Undoing it needs two API calls, and there was no tool for them.
//
// What happened next is the reason this is a command rather than a shell line.
// `gh api` uses the keyring token, which carries no packages scope, so it
// answered 403. The token that CAN do the job lives in a file, and the obvious
// ways to hand it to curl — an environment variable, a --header argument, a
// URL — are exactly the ways a secret reaches a process listing, a shell
// history and a log. A machine here has lost a token to a URL twice.
//
// So the token goes where it goes in every other tool in this repository: read
// from a file into a variable, set as a request header, and never anywhere a
// second process can see it.
//
// # What it refuses
//
// It refuses to delete without --yes, printing instead exactly what it would
// remove. A deleted package version cannot be restored, and the registry is
// shared: anyone who has already resolved that version keeps working only
// until their next clean install.
//
// It refuses a tag that matches no version, and one that matches more than
// one — a tag naming two things is not something to guess about.
//
// It NAMES the other tags on the version it is about to delete. A container
// version is a manifest, and several tags can point at one: deleting `2.1.91`
// can take `2.1` and `v2` with it. That is the surprise worth printing before
// the irreversible step, not after.
//
// It checks the scope BEFORE asking, so a missing permission is reported as
// what it is rather than as a 403 from an endpoint nobody has heard of.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/ghauth"
)

// apiBase is a variable so a test can answer as GitHub would. What this tool
// does with the answer is the part worth testing, and it guards something
// irreversible.
var apiBase = "https://api.github.com"

// version is one published version of a package, reduced to what a decision
// needs: which one it is, what it is called, and when it appeared.
type version struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Metadata  struct {
		Container struct {
			Tags []string `json:"tags"`
		} `json:"container"`
	} `json:"metadata"`
}

func (v version) tags() string {
	if len(v.Metadata.Container.Tags) == 0 {
		return "(untagged)"
	}
	return strings.Join(v.Metadata.Container.Tags, ", ")
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  ghpkg list   <org> <package>
  ghpkg delete <org> <package> <tag> [--yes]

  <package> is the name as the registry holds it, e.g. packages/libjpeg-turbo.org

flags:
  --token <path>   the credential to use (default: ~/.github-token)
  --yes            actually delete; without it, ghpkg prints what it would do
`)
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	verb, rest := args[0], args[1:]

	fset := flag.NewFlagSet("ghpkg", flag.ContinueOnError)
	fset.SetOutput(stderr)
	tokenPath := fset.String("token", ghauth.DefaultPath(), "credential file")
	yes := fset.Bool("yes", false, "actually delete")
	// Flags anywhere, not only first. Go's flag package stops at the first
	// positional, so `ghpkg delete org pkg tag --yes` would parse no flags at
	// all — and a --token that was silently ignored fails with a message about
	// the wrong file. On a command that deletes things, an argument order
	// people get wrong under pressure is a defect, not a convention.
	var pos, flags []string
	for i, a := range rest {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, rest[i:]...)
			break
		}
		pos = append(pos, a)
	}
	if err := fset.Parse(flags); err != nil {
		return 2
	}
	pos = append(pos, fset.Args()...)

	switch verb {
	case "list":
		if len(pos) != 2 {
			usage(stderr)
			return 2
		}
		return runList(*tokenPath, pos[0], pos[1], stdout, stderr)
	case "delete":
		if len(pos) != 3 {
			usage(stderr)
			return 2
		}
		return runDelete(*tokenPath, pos[0], pos[1], pos[2], *yes, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "ghpkg: unknown command:", verb)
		usage(stderr)
		return 2
	}
}

func runList(tokenPath, org, pkg string, stdout, stderr io.Writer) int {
	tok, err := ghauth.Read(tokenPath)
	if err != nil {
		fmt.Fprintln(stderr, "ghpkg:", err)
		return 1
	}
	if code := requireScopes(tok, []string{"read:packages"}, tokenPath, stderr); code != 0 {
		return code
	}
	vs, err := listVersions(tok, org, pkg)
	if err != nil {
		fmt.Fprintln(stderr, "ghpkg:", err)
		return 1
	}
	for _, v := range vs {
		fmt.Fprintf(stdout, "%-12d %-19s %s\n", v.ID, v.CreatedAt.UTC().Format("2006-01-02 15:04:05"), v.tags())
	}
	fmt.Fprintf(stdout, "\n%d version(s)\n", len(vs))
	return 0
}

func runDelete(tokenPath, org, pkg, tag string, yes bool, stdout, stderr io.Writer) int {
	tok, err := ghauth.Read(tokenPath)
	if err != nil {
		fmt.Fprintln(stderr, "ghpkg:", err)
		return 1
	}
	// Both, and before asking: delete needs read to find what to delete, and a
	// 403 from a versions endpoint is not a sentence anybody should have to
	// decode.
	if code := requireScopes(tok, []string{"read:packages", "delete:packages"}, tokenPath, stderr); code != 0 {
		return code
	}
	vs, err := listVersions(tok, org, pkg)
	if err != nil {
		fmt.Fprintln(stderr, "ghpkg:", err)
		return 1
	}
	var match []version
	for _, v := range vs {
		for _, t := range v.Metadata.Container.Tags {
			if t == tag {
				match = append(match, v)
				break
			}
		}
	}
	switch len(match) {
	case 0:
		fmt.Fprintf(stderr, "ghpkg: no version of %s/%s is tagged %s\n", org, pkg, tag)
		return 1
	case 1:
	default:
		fmt.Fprintf(stderr, "ghpkg: %d versions are tagged %s, which is not something to guess about:\n", len(match), tag)
		for _, v := range match {
			fmt.Fprintf(stderr, "    %d  %s\n", v.ID, v.tags())
		}
		return 1
	}
	v := match[0]

	fmt.Fprintf(stdout, "would delete from %s/%s:\n    id %d, created %s\n    tags: %s\n",
		org, pkg, v.ID, v.CreatedAt.UTC().Format("2006-01-02 15:04:05"), v.tags())
	// A container version is a MANIFEST and several tags can point at one, so
	// deleting 2.1.91 can take 2.1 and v2 with it. Said before the
	// irreversible step, not after.
	if others := otherTags(v, tag); len(others) > 0 {
		fmt.Fprintf(stdout, "\nthis version also carries %s — deleting it removes those tags too\n",
			strings.Join(others, ", "))
	}
	if !yes {
		fmt.Fprintln(stdout, "\nnothing done. Pass --yes to delete it; a deleted version cannot be restored.")
		return 2
	}
	if err := deleteVersion(tok, org, pkg, v.ID); err != nil {
		fmt.Fprintln(stderr, "ghpkg:", err)
		return 1
	}
	fmt.Fprintf(stdout, "\ndeleted %d\n", v.ID)
	return 0
}

// otherTags is what would go with it.
func otherTags(v version, tag string) []string {
	var out []string
	for _, t := range v.Metadata.Container.Tags {
		if t != tag {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// requireScopes reports a missing permission as what it IS, before any request
// is made. Counting the hierarchy, so a token ticked write:packages is not
// told it cannot read them.
func requireScopes(tok string, want []string, tokenPath string, stderr io.Writer) int {
	have, err := tokenScopes(tok)
	if err != nil {
		fmt.Fprintln(stderr, "ghpkg:", err)
		return 1
	}
	if miss := ghauth.Missing(have, want); len(miss) > 0 {
		fmt.Fprintf(stderr, "ghpkg: %s lacks %s — tick it at https://github.com/settings/tokens\n",
			tokenPath, strings.Join(miss, ", "))
		return 1
	}
	return 0
}

func tokenScopes(tok string) ([]string, error) {
	resp, err := do(http.MethodGet, apiBase+"/user", tok)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s — the token is probably expired or revoked", resp.Status)
	}
	var out []string
	for _, s := range strings.Split(resp.Header.Get("X-OAuth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// listVersions walks every page: a package with 120 versions must not report
// the first 100 as all of them, which is how a tag gets called absent.
func listVersions(tok, org, pkg string) ([]version, error) {
	var all []version
	for page := 1; page <= 50; page++ {
		u := fmt.Sprintf("%s/orgs/%s/packages/container/%s/versions?per_page=100&page=%d",
			apiBase, url.PathEscape(org), url.PathEscape(pkg), page)
		resp, err := do(http.MethodGet, u, tok)
		if err != nil {
			return nil, err
		}
		body, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			return nil, rerr
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("listing %s/%s: GitHub answered %s", org, pkg, resp.Status)
		}
		var vs []version
		if err := json.Unmarshal(body, &vs); err != nil {
			return nil, fmt.Errorf("reading GitHub's answer: %w", err)
		}
		all = append(all, vs...)
		if len(vs) < 100 {
			break
		}
	}
	return all, nil
}

func deleteVersion(tok, org, pkg string, id int64) error {
	u := fmt.Sprintf("%s/orgs/%s/packages/container/%s/versions/%d",
		apiBase, url.PathEscape(org), url.PathEscape(pkg), id)
	resp, err := do(http.MethodDelete, u, tok)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deleting version %d: GitHub answered %s", id, resp.Status)
	}
	return nil
}

// do makes the request with the token in a HEADER and nowhere else. A
// transport error can quote the URL but never a header, so there is nothing
// here that could carry it into a message.
func do(method, u, tok string) (*http.Response, error) {
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return (&http.Client{Timeout: 30 * time.Second}).Do(req)
}
