package main

// Catching up on tags that have no GitHub release (go-gitsafe/gitsafe#59).
//
// Until v0.18.0 ghrelease pushed the tag and stopped, so hundreds of the
// fleet's tags have no release. The user's decision (2026-10-26): catch up
// as repositories are worked on, and settle the recent ones now. So:
//
//   - every ghrelease run that releases a new tag also creates the missing
//     releases of that SAME repository's older tags, oldest first, at most
//     -backfill-max (20) per run; -no-backfill turns it off;
//   - `ghrelease backfill -since 14d` does the recent tags of the fleet,
//     repositories taken from fleetfind's inventory and restricted to those
//     pushed within the window.
//
// A backfilled release never moves a tag, never takes the Latest badge
// (make_latest "false"), and says what it is: "Release backfilled on <date>;
// tag created <date>", then the commits since the previous tag.
//
// The API budget is the user's, shared with the fleet's Renovate runners
// (each sweep that emptied it made Renovate fail at initialisation). One
// GraphQL call per repository reads its tags, their dates and its releases;
// each backfilled release costs one compare call and one create. Both
// budgets are read from the answers themselves (rateLimit in the GraphQL
// response, X-RateLimit-Remaining on REST answers), never from /rate_limit,
// which shows one token's bucket and has read 5000/5000 while every real call
// was refused. Below the margin the run stops and says so.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/cliflags"
	"github.com/spf13/cobra"
)

const (
	defaultBackfillMax = 20
	defaultMargin      = 1000
	maxNoteCommits     = 100
)

// now is a seam for the tests.
var now = time.Now

// tagInfo is one tag of a repository.
type tagInfo struct {
	Name string
	Date time.Time // the tagged commit's date (the tagger's for an annotated tag)
}

// repoState is what one GraphQL call says about a repository.
type repoState struct {
	tags     []tagInfo       // newest first, at most 100
	released map[string]bool // tag names that have a release
	// oldestRelease bounds what is known: with 100 releases listed, a tag
	// older than the oldest listed may have a release not listed.
	oldestRelease time.Time
	truncated     bool
}

// budget tracks the two rate limits as the answers report them.
type budget struct {
	margin   int
	rest     int // -1: not seen yet
	graphql  int
	restN    int // calls made
	graphqlN int
}

func newBudget(margin int) *budget { return &budget{margin: margin, rest: -1, graphql: -1} }

func (b *budget) noteREST(h http.Header) {
	b.restN++
	if v, err := strconv.Atoi(h.Get("X-RateLimit-Remaining")); err == nil {
		b.rest = v
	}
}

// low says why the run must stop, or "".
func (b *budget) low() string {
	switch {
	case b.rest >= 0 && b.rest < b.margin:
		return fmt.Sprintf("the REST budget is down to %d (margin %d): stopping, Renovate needs the rest", b.rest, b.margin)
	case b.graphql >= 0 && b.graphql < b.margin:
		return fmt.Sprintf("the GraphQL budget is down to %d (margin %d): stopping", b.graphql, b.margin)
	}
	return ""
}

const stateQuery = `query($o:String!,$n:String!){
  repository(owner:$o,name:$n){
    refs(refPrefix:"refs/tags/",first:100,orderBy:{field:TAG_COMMIT_DATE,direction:DESC}){
      nodes{name target{__typename ... on Commit{committedDate} ... on Tag{tagger{date}}}}}
    releases(first:100,orderBy:{field:CREATED_AT,direction:DESC}){nodes{tagName createdAt}}
  }
  rateLimit{remaining}
}`

// readState asks GitHub for a repository's tags and releases, in one call.
func readState(token, repo string, b *budget) (repoState, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return repoState{}, fmt.Errorf("%q is not owner/repo", repo)
	}
	resp, err := api(token, http.MethodPost, apiBase+"/graphql",
		map[string]any{"query": stateQuery, "variables": map[string]string{"o": owner, "n": name}})
	if err != nil {
		return repoState{}, err
	}
	defer resp.Body.Close()
	b.graphqlN++
	if resp.StatusCode != http.StatusOK {
		return repoState{}, refused("reading the tags and releases", resp)
	}
	var out struct {
		Data struct {
			Repository *struct {
				Refs struct {
					Nodes []struct {
						Name   string
						Target struct {
							Typename      string `json:"__typename"`
							CommittedDate time.Time
							Tagger        *struct{ Date time.Time }
						}
					}
				}
				Releases struct {
					Nodes []struct {
						TagName   string
						CreatedAt time.Time
					}
				}
			}
			RateLimit *struct{ Remaining int }
		}
		Errors []struct{ Message string }
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return repoState{}, fmt.Errorf("reading the tags and releases: %w", err)
	}
	if out.Data.RateLimit != nil {
		b.graphql = out.Data.RateLimit.Remaining
	}
	if len(out.Errors) > 0 {
		return repoState{}, fmt.Errorf("reading the tags and releases: %s", out.Errors[0].Message)
	}
	if out.Data.Repository == nil {
		return repoState{}, fmt.Errorf("%s: no such repository", repo)
	}
	st := repoState{released: map[string]bool{}}
	for _, n := range out.Data.Repository.Refs.Nodes {
		d := n.Target.CommittedDate
		if n.Target.Tagger != nil {
			d = n.Target.Tagger.Date
		}
		st.tags = append(st.tags, tagInfo{Name: n.Name, Date: d})
	}
	rels := out.Data.Repository.Releases.Nodes
	for _, r := range rels {
		st.released[r.TagName] = true
	}
	if len(rels) == 100 {
		st.truncated = true
		st.oldestRelease = rels[len(rels)-1].CreatedAt
	}
	return st, nil
}

// missing returns the tags without a release, oldest first, skipping skip
// and, when the release list was truncated, the tags older than what it
// covers (they may have a release that was not listed).
func (st repoState) missing(skip string, since time.Time) []tagInfo {
	var out []tagInfo
	for _, t := range st.tags {
		if t.Name == skip || st.released[t.Name] || !strings.HasPrefix(t.Name, "v") {
			continue
		}
		if st.truncated && t.Date.Before(st.oldestRelease) {
			continue
		}
		if !since.IsZero() && t.Date.Before(since) {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}

// previous returns the tag just older than t, or "".
func (st repoState) previous(t tagInfo) string {
	best := ""
	var bestDate time.Time
	for _, o := range st.tags {
		if o.Name != t.Name && o.Date.Before(t.Date) && (best == "" || o.Date.After(bestDate)) {
			best, bestDate = o.Name, o.Date
		}
	}
	return best
}

// backfillNotes is the body of a backfilled release.
func backfillNotes(token, repo string, st repoState, t tagInfo, b *budget) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Release backfilled on %s; tag created %s.\n\n", now().UTC().Format("2006-01-02"), t.Date.UTC().Format("2006-01-02"))
	prev := st.previous(t)
	if prev == "" {
		sb.WriteString("The first tag of the repository.\n")
		return sb.String(), nil
	}
	resp, err := api(token, http.MethodGet, fmt.Sprintf("%s/repos/%s/compare/%s...%s", apiBase, repo, prev, t.Name), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b.noteREST(resp.Header)
	if resp.StatusCode != http.StatusOK {
		return "", refused("comparing "+prev+" with "+t.Name, resp)
	}
	var cmp struct {
		TotalCommits int `json:"total_commits"`
		Commits      []struct {
			SHA    string
			Commit struct{ Message string }
		}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&cmp); err != nil {
		return "", err
	}
	fmt.Fprintf(&sb, "Commits since %s:\n\n", prev)
	for i, c := range cmp.Commits {
		if i == maxNoteCommits {
			fmt.Fprintf(&sb, "- … and %d more\n", cmp.TotalCommits-maxNoteCommits)
			break
		}
		subject, _, _ := strings.Cut(c.Commit.Message, "\n")
		fmt.Fprintf(&sb, "- %s %s\n", short(c.SHA), subject)
	}
	return sb.String(), nil
}

// createBackfillRelease publishes the release of an existing old tag; it
// never takes the Latest badge.
func createBackfillRelease(token, repo, tag, notes string, b *budget) error {
	resp, err := api(token, http.MethodPost, fmt.Sprintf("%s/repos/%s/releases", apiBase, repo),
		map[string]any{"tag_name": tag, "name": tag, "body": notes, "make_latest": "false"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b.noteREST(resp.Header)
	if resp.StatusCode != http.StatusCreated {
		return refused("GitHub refused the release of "+tag, resp)
	}
	return nil
}

// backfillRepo creates at most max missing releases of repo, oldest first,
// for tags dated after since (zero: any). It reports what it found and did.
func backfillRepo(token, repo, skip string, since time.Time, max int, dry bool, b *budget, out io.Writer) (found, created int, err error) {
	st, err := readState(token, repo, b)
	if err != nil {
		return 0, 0, err
	}
	todo := st.missing(skip, since)
	found = len(todo)
	for _, t := range todo {
		if created == max {
			fmt.Fprintf(out, "%s: %d more tag(s) without a release, left for a later run (-backfill-max %d)\n", repo, found-created, max)
			break
		}
		if why := b.low(); why != "" {
			return found, created, errors.New(why)
		}
		if dry {
			fmt.Fprintf(out, "%s: would release %s (tagged %s)\n", repo, t.Name, t.Date.UTC().Format("2006-01-02"))
			created++
			continue
		}
		notes, err := backfillNotes(token, repo, st, t, b)
		if err != nil {
			return found, created, err
		}
		if err := createBackfillRelease(token, repo, t.Name, notes, b); err != nil {
			return found, created, err
		}
		fmt.Fprintf(out, "%s: released %s (backfilled; tagged %s)\n", repo, t.Name, t.Date.UTC().Format("2006-01-02"))
		created++
	}
	return found, created, nil
}

// parseSince reads "14d" or a Go duration.
func parseSince(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 || n > 3650 {
			return 0, fmt.Errorf("-since %q: want Nd (1..3650) or a Go duration", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("-since %q: want Nd or a Go duration", s)
	}
	return d, nil
}

// inventory is fleetfind's cache: every organisation's repositories.
type inventory struct {
	Taken time.Time `json:"taken"`
	Repos []struct {
		Org      string    `json:"org"`
		Name     string    `json:"name"`
		Archived bool      `json:"archived"`
		Pushed   time.Time `json:"pushed"`
	} `json:"repos"`
}

func defaultInventory() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "fleetfind", "inventory.json")
}

type backfillOptions struct {
	since, tokenFile, inventory, orgs string
	dry                               bool
	perRepo, margin                   int
}

// backfillCmd is `ghrelease backfill`, a real subcommand so that
// `ghrelease backfill -h` is asked of backfill and not of the release verb.
func backfillCmd() *cobra.Command {
	var o backfillOptions
	c := &cobra.Command{
		Use:   "backfill [flags]",
		Short: "Create the missing releases of recent tags across the fleet",
		// Operands were never read and still are not.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if code := runBackfill(o, cmd.OutOrStdout(), cmd.ErrOrStderr()); code != 0 {
				return cliflags.Status(code)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&o.since, "since", "14d", "the window: tags created within it (Nd or a Go duration)")
	f.StringVar(&o.tokenFile, "token-file", "", "the file holding the token (default ~/"+defaultToken+")")
	f.StringVar(&o.inventory, "inventory", defaultInventory(), "fleetfind's inventory (refresh it with `fleetfind -refresh` first)")
	f.StringVar(&o.orgs, "orgs", "", "comma-separated organisations to limit the run to (default: every one in the inventory)")
	f.BoolVar(&o.dry, "dry-run", false, "count and name what would be released, create nothing")
	f.IntVar(&o.perRepo, "max-per-repo", defaultBackfillMax, "at most this many releases per repository")
	f.IntVar(&o.margin, "margin", defaultMargin, "stop when the REST or GraphQL budget falls below this")
	return c
}

func runBackfill(o backfillOptions, stdout, stderr io.Writer) int {
	sinceS, tokenFile, inv, orgs, dry, perRepo, margin :=
		&o.since, &o.tokenFile, &o.inventory, &o.orgs, &o.dry, &o.perRepo, &o.margin
	since, err := parseSince(*sinceS)
	if err != nil {
		fmt.Fprintf(stderr, "ghrelease backfill: %v\n", err)
		return 2
	}
	raw, err := os.ReadFile(*inv)
	if err != nil {
		fmt.Fprintf(stderr, "ghrelease backfill: cannot read fleetfind's inventory: %v\n", err)
		return 2
	}
	var in inventory
	if err := json.Unmarshal(raw, &in); err != nil || len(in.Repos) == 0 {
		fmt.Fprintf(stderr, "ghrelease backfill: the inventory %s is unreadable or empty\n", *inv)
		return 2
	}
	from := now().Add(-since)
	if in.Taken.Before(now().Add(-24 * time.Hour)) {
		fmt.Fprintf(stderr, "ghrelease backfill: the inventory is from %s: a repository pushed since then is not seen (fleetfind -refresh)\n",
			in.Taken.Format(time.RFC3339))
	}
	want := map[string]bool{}
	for _, o := range strings.Split(*orgs, ",") {
		if o = strings.TrimSpace(o); o != "" {
			want[o] = true
		}
	}
	var repos []string
	for _, r := range in.Repos {
		if r.Archived || r.Pushed.Before(from) || len(want) > 0 && !want[r.Org] {
			continue
		}
		repos = append(repos, r.Org+"/"+r.Name)
	}
	sort.Strings(repos)
	tok, err := token(*tokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "ghrelease backfill: %v\n", err)
		return 1
	}
	b := newBudget(*margin)
	found, created, failed, scanned := 0, 0, 0, 0
	status := 0
	for _, repo := range repos {
		if why := b.low(); why != "" {
			fmt.Fprintf(stderr, "ghrelease backfill: %s; %d of %d repositories scanned\n", why, scanned, len(repos))
			status = 1
			break
		}
		f, c, err := backfillRepo(tok, repo, "", from, *perRepo, *dry, b, stdout)
		scanned++
		found += f
		created += c
		if err != nil {
			failed++
			fmt.Fprintf(stderr, "ghrelease backfill: %s: %v\n", repo, err)
			if b.low() != "" {
				continue // the loop's check says it and stops
			}
		}
	}
	verb := "created"
	if *dry {
		verb = "would create"
	}
	fmt.Fprintf(stdout, "%d repositories pushed within %s scanned (of %d); %d recent tag(s) without a release; %s %d; "+
		"%d repository error(s); API calls: %d GraphQL, %d REST\n",
		scanned, *sinceS, len(repos), found, verb, created, failed, b.graphqlN, b.restN)
	if failed > 0 {
		status = 1
	}
	return status
}
