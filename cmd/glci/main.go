// glci says how the latest GitLab pipeline of a branch, tag or merge request
// went, job by job, and shows the end of every failed job's log — with any
// secret in it masked.
//
//	glci                      this clone's project, current branch
//	glci main                 another branch or a tag
//	glci '!1'                 merge request !1
//	glci -watch '!1'          wait until the pipeline is finished
//	glci -project resinfo/gt/gt-cloud/docs -host plmlab.math.cnrs.fr main
//
// Exit status, so a script cannot mistake silence for success:
//
//	0  the pipeline succeeded
//	1  it failed or was canceled
//	3  it is not finished (created, pending, running, manual…)
//	4  there is NO pipeline for that ref: nothing ran, and nothing passed
//	2  usage, or the instance could not be asked
//
// "No pipeline" has its own status because it is the dangerous one. A merge
// request whose pipeline never started reads as green to anything that only
// looks for failures — that is how a change was once merged over checks that
// had never run.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/glauth"
	"github.com/go-gitsafe/gitsafe/redact"
)

var osExit = os.Exit

// Variables so a test can point the tool at a fake instance and not wait.
var (
	newClient = glauth.New
	sleep     = time.Sleep
	homeDir   = os.UserHomeDir
)

const (
	exitSuccess    = 0
	exitFailed     = 1
	exitUsage      = 2
	exitUnfinished = 3
	exitNoPipeline = 4
)

func main() { osExit(run(os.Args[1:], ".", os.Stdout, os.Stderr)) }

type pipeline struct {
	ID     int    `json:"id"`
	IID    int    `json:"iid"`
	Status string `json:"status"`
	Ref    string `json:"ref"`
	SHA    string `json:"sha"`
	Source string `json:"source"`
	WebURL string `json:"web_url"`
}

type job struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Stage    string  `json:"stage"`
	Status   string  `json:"status"`
	Duration float64 `json:"duration"`
	WebURL   string  `json:"web_url"`
	// AllowFailure jobs fail without failing the pipeline; they are shown, and
	// their log too, but said to be allowed.
	AllowFailure bool `json:"allow_failure"`
}

func run(args []string, dir string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("glci", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", "", "GitLab instance (default: this clone's origin)")
	project := fs.String("project", "", "project path, e.g. group/sub/project (default: this clone's origin)")
	file := fs.String("f", "", "token file (default: ~/.gitlab-token-<host>, else ~/.gitlab-token)")
	watch := fs.Bool("watch", false, "wait until the pipeline is finished")
	every := fs.Duration("every", 20*time.Second, "polling interval with -watch")
	tail := fs.Int("log", 40, "lines of each failed job's log to show (0: none)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "glci: at most one ref or !merge-request")
		return exitUsage
	}
	ref := fs.Arg(0)

	if *host == "" || *project == "" {
		r, err := glauth.Origin(dir)
		if err != nil {
			fmt.Fprintf(stderr, "glci: %v\n", err)
			return exitUsage
		}
		if *host == "" {
			*host = r.Host
		}
		if *project == "" {
			*project = r.Project
		}
	}
	if ref == "" {
		if ref = glauth.CurrentBranch(dir); ref == "" {
			fmt.Fprintln(stderr, "glci: HEAD is detached; name a branch, a tag or !merge-request")
			return exitUsage
		}
	}

	home, err := homeDir()
	if err != nil {
		fmt.Fprintf(stderr, "glci: no home directory: %v\n", err)
		return exitUsage
	}
	tok, err := glauth.Read(glauth.TokenPath(home, *host, *file))
	if err != nil {
		fmt.Fprintf(stderr, "glci: %v\n", err)
		return exitUsage
	}
	c := newClient(*host, tok)
	mask := redact.New(tok)
	pid := glauth.ProjectID(*project)

	for {
		p, err := latest(c, pid, ref)
		if err != nil {
			fmt.Fprintf(stderr, "glci: %s\n", mask.String(err.Error()))
			return exitUsage
		}
		if p == nil {
			fmt.Fprintf(stdout, "%s %s: no pipeline — nothing ran, so nothing passed\n", *project, ref)
			return exitNoPipeline
		}
		status := verdict(p.Status)
		if status == exitUnfinished && *watch {
			fmt.Fprintf(stderr, "glci: pipeline %d is %s; checking again in %s\n", p.ID, p.Status, *every)
			sleep(*every)
			continue
		}
		jobs, err := jobsOf(c, pid, p.ID)
		if err != nil {
			fmt.Fprintf(stderr, "glci: %s\n", mask.String(err.Error()))
			return exitUsage
		}
		report(stdout, *project, ref, p, jobs)
		if *tail > 0 {
			for _, j := range jobs {
				if j.Status != "failed" {
					continue
				}
				showLog(stdout, c, pid, j, *tail, mask)
			}
		}
		return status
	}
}

// latest is the newest pipeline of ref, or of merge request "!N"; nil when
// there is none.
func latest(c *glauth.Client, pid, ref string) (*pipeline, error) {
	var ps []pipeline
	var err error
	if iid, ok := strings.CutPrefix(ref, "!"); ok {
		if _, convErr := strconv.Atoi(iid); convErr != nil {
			return nil, fmt.Errorf("%q is not a merge request number", ref)
		}
		err = c.GetJSON("/projects/"+pid+"/merge_requests/"+iid+"/pipelines?per_page=1", &ps)
	} else {
		err = c.GetJSON("/projects/"+pid+"/pipelines?per_page=1&order_by=id&sort=desc&ref="+queryEscape(ref), &ps)
	}
	if err != nil {
		var r *glauth.Refusal
		if errors.As(err, &r) && r.Status == 404 {
			return nil, fmt.Errorf("%v (check the project path and the ref)", err)
		}
		return nil, err
	}
	if len(ps) == 0 {
		return nil, nil
	}
	return &ps[0], nil
}

func jobsOf(c *glauth.Client, pid string, pipelineID int) ([]job, error) {
	var all []job
	for page := 1; ; page++ {
		var js []job
		if err := c.GetJSON(fmt.Sprintf("/projects/%s/pipelines/%d/jobs?per_page=100&page=%d", pid, pipelineID, page), &js); err != nil {
			return nil, err
		}
		all = append(all, js...)
		if len(js) < 100 {
			return all, nil
		}
	}
}

// verdict maps a pipeline status onto the exit status. Anything not known to be
// finished counts as unfinished, never as success.
func verdict(status string) int {
	switch status {
	case "success":
		return exitSuccess
	case "failed", "canceled", "canceling":
		return exitFailed
	}
	return exitUnfinished
}

func report(w io.Writer, project, ref string, p *pipeline, jobs []job) {
	sha := p.SHA
	if len(sha) > 8 {
		sha = sha[:8]
	}
	fmt.Fprintf(w, "%s %s: pipeline %d %s (%s, %s)\n", project, ref, p.ID, strings.ToUpper(p.Status), p.Source, sha)
	fmt.Fprintf(w, "  %s\n", p.WebURL)
	for _, j := range jobs {
		note := ""
		if j.AllowFailure && j.Status == "failed" {
			note = "  (allowed to fail)"
		}
		fmt.Fprintf(w, "  %-10s %-24s %-9s %6.0fs%s\n", j.Stage, j.Name, j.Status, j.Duration, note)
	}
}

// ansi and section markers make a raw GitLab job log unreadable in a terminal
// transcript: colour codes, and "section_start:<ts>:<name>" collapsible headers.
var (
	ansi    = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\[0K`)
	section = regexp.MustCompile(`section_(start|end):[0-9]+:[A-Za-z0-9_.-]+(\[[^\]]*\])?\r?`)
	// stamp is the per-line prefix of logs from instances with job log
	// timestamps on (GitLab 17 onward): "2026-10-04T15:41:36.475297Z 01O ",
	// an RFC 3339 time, a stream number and O/E for stdout/stderr, "+" when
	// the line continues the previous one. Measured on plmlab.math.cnrs.fr.
	stamp = regexp.MustCompile(`(?m)^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z [0-9]{2}[OE]\+? ?`)
)

func showLog(w io.Writer, c *glauth.Client, pid string, j job, n int, mask *redact.Redactor) {
	b, err := c.Get(fmt.Sprintf("/projects/%s/jobs/%d/trace", pid, j.ID))
	fmt.Fprintf(w, "\n--- %s (%s) — last %d lines of the log\n", j.Name, j.WebURL, n)
	if err != nil {
		fmt.Fprintf(w, "    (log not readable: %s)\n", mask.String(err.Error()))
		return
	}
	text := section.ReplaceAllString(ansi.ReplaceAllString(string(b), ""), "")
	text = stamp.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		// A job log is the most likely place for a secret to surface: a
		// command that echoed its environment, a URL with a token in it. The
		// token used here is masked by identity, anything else by shape.
		if i := strings.LastIndex(l, "\r"); i >= 0 {
			l = l[i+1:]
		}
		fmt.Fprintf(w, "    %s\n", mask.String(l))
	}
}

// queryEscape escapes a ref for a query string; refs can hold "/" and "+".
func queryEscape(s string) string {
	r := strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", "#", "%23", " ", "%20", "/", "%2F", "?", "%3F")
	return r.Replace(s)
}
