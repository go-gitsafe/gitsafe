// Command ghrunner registers a self-hosted GitHub Actions runner without the
// registration token ever reaching a command line.
//
//	ghrunner -host linux1@10.0.0.1 -name linuxone-s390x -labels S390X go-pkgx/packages
//	ghrunner -host … -name … -remove          # unregister, on both sides
//
// # Why this is a command
//
// actions/runner's config.sh takes the token as `--token`, and a command line
// reaches the process list, the shell history and every log of what ran. That
// is the one thing this repository exists to prevent, and the runner's own
// documentation tells you to do it.
//
// The obvious fix does not work. Piping the token into config.sh fails:
//
//	What is your runner register token? Cannot read keys when either
//	application does not have a console or when console input has been
//	redirected. Try Console.Read.
//
// It reads the token with Console.ReadKey — the masked-input call — which
// wants a terminal. So the safe path is a PTY with the echo turned OFF, and it
// is fiddly enough that the second person to need it will reach for --token
// instead. Measured on 2026-09-29, registering one s390x runner: three
// attempts, of which the first two failed for reasons that had nothing to do
// with GitHub.
//
// The PTY is ssh's. That is not a limitation dressed up as a choice — a
// self-hosted runner is a machine you administer over ssh, and `ssh -tt`
// allocates one remotely with no cgo and no dependency here.
//
// # What it does besides
//
//   - takes the registration token from the API with the token in
//     ~/.github-token, never from an argument;
//   - removes a stale OFFLINE registration of the same name first, because
//     GitHub keeps the entry when the machine is destroyed and the name cannot
//     be reused while it is there;
//   - turns the terminal's echo off before config.sh runs, so the token is not
//     reflected into anything that captures the session;
//   - redacts everything it prints, by shape, through the same redactor the
//     push guard uses;
//   - reads the registration back from the API and says what LABELS it got,
//     because a runner whose labels do not match a workflow's `runs-on` is
//     registered and idle, which looks like nothing at all.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/ghauth"
	"github.com/go-gitsafe/gitsafe/redact"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

type options struct {
	host   string
	name   string
	labels string
	group  string
	work   string
	remove bool
	repo   string
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ghrunner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.host, "host", "", "the machine to register, as ssh reaches it (user@host). REQUIRED: the terminal config.sh needs is ssh's")
	fs.StringVar(&o.name, "name", "", "the runner's name. Defaults to the host's own hostname, as config.sh would")
	fs.StringVar(&o.labels, "labels", "", "extra labels, comma separated. self-hosted, the OS and the architecture are added by the runner itself")
	fs.StringVar(&o.group, "group", "Default", "runner group")
	fs.StringVar(&o.work, "work", "_work", "the runner's work directory, relative to its install")
	fs.BoolVar(&o.remove, "remove", false, "unregister instead: config.sh remove on the machine, and the API entry here")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: ghrunner -host user@machine [-name n] [-labels a,b] owner/repo")
		return 2
	}
	o.repo = fs.Arg(0)
	if o.host == "" {
		fmt.Fprintln(stderr, "ghrunner: -host is required — config.sh needs a terminal, and ssh is what allocates one")
		return 2
	}
	if !strings.Contains(o.repo, "/") {
		fmt.Fprintf(stderr, "ghrunner: %q is not owner/repo\n", o.repo)
		return 2
	}

	token, err := ghauth.Read(ghauth.DefaultPath())
	if err != nil {
		fmt.Fprintln(stderr, "ghrunner:", err)
		return 1
	}
	// Everything printed from here on goes through this. The registration
	// token is not in it — it is never held in a string this process prints —
	// but ssh and config.sh both echo things, and the redactor knows the
	// shapes.
	red := redact.New(token)
	out := &redactWriter{w: stdout, r: red}
	errOut := &redactWriter{w: stderr, r: red}

	c := &client{token: token, repo: o.repo}
	if o.remove {
		return doRemove(c, o, out, errOut)
	}
	return doRegister(c, o, out, errOut)
}

func doRegister(c *client, o options, stdout, stderr io.Writer) int {
	// A destroyed machine leaves its registration behind, OFFLINE, and the
	// name cannot be reused while it is there. Only an offline one is removed:
	// taking out a runner that is online would stop work somebody is running.
	if o.name != "" {
		switch id, status, err := c.findRunner(o.name); {
		case err != nil:
			fmt.Fprintln(stderr, "ghrunner: looking for an existing runner:", err)
			return 1
		case id != 0 && status == "online":
			fmt.Fprintf(stderr, "ghrunner: %s is already registered and ONLINE (id %d). "+
				"Remove it deliberately, with -remove, rather than through this.\n", o.name, id)
			return 1
		case id != 0:
			fmt.Fprintf(stdout, "removing the stale %s registration (id %d, %s)\n", o.name, id, status)
			if err := c.deleteRunner(id); err != nil {
				fmt.Fprintln(stderr, "ghrunner:", err)
				return 1
			}
		}
	}

	regTok, err := c.registrationToken()
	if err != nil {
		fmt.Fprintln(stderr, "ghrunner:", err)
		return 1
	}

	cfg := []string{
		"--url", "https://github.com/" + o.repo,
		"--runnergroup", o.group,
		"--work", o.work,
		"--replace",
	}
	if o.name != "" {
		cfg = append(cfg, "--name", o.name)
	}
	if o.labels != "" {
		cfg = append(cfg, "--labels", o.labels)
	}
	// NOT --token. That is the whole point.
	if code := c.sshConfigure(o.host, cfg, regTok, stdout, stderr); code != 0 {
		return code
	}

	if code := sshRun(o.host, "cd ~/actions-runner && sudo ./svc.sh install \"$(id -un)\" && sudo ./svc.sh start", stdout, stderr); code != 0 {
		fmt.Fprintln(stderr, "ghrunner: the runner is registered but its service did not start")
		return code
	}
	return c.report(o.name, stdout, stderr)
}

func doRemove(c *client, o options, stdout, stderr io.Writer) int {
	regTok, err := c.removeToken()
	if err != nil {
		fmt.Fprintln(stderr, "ghrunner:", err)
		return 1
	}
	sshRun(o.host, "cd ~/actions-runner && sudo ./svc.sh stop && sudo ./svc.sh uninstall", stdout, stderr)
	if code := c.sshConfigure(o.host, []string{"remove"}, regTok, stdout, stderr); code != 0 {
		return code
	}
	// And the entry here, which config.sh remove usually clears but does not
	// when the machine and the API have already parted.
	if id, _, err := c.findRunner(o.name); err == nil && id != 0 {
		if err := c.deleteRunner(id); err != nil {
			fmt.Fprintln(stderr, "ghrunner:", err)
			return 1
		}
		fmt.Fprintf(stdout, "removed the %s registration (id %d)\n", o.name, id)
	}
	return 0
}

// report reads the registration back and says what LABELS it got.
//
// A runner registered with the wrong labels is online and idle, and a workflow
// waiting for it looks like a workflow nobody dispatched. The labels are the
// one thing worth printing.
func (c *client) report(name string, stdout, stderr io.Writer) int {
	if name == "" {
		fmt.Fprintln(stdout, "registered. No -name was given, so it took the machine's hostname; "+
			"check its labels against the workflow's runs-on.")
		return 0
	}
	// The runner takes a moment to connect after the service starts.
	for i := 0; i < 10; i++ {
		id, status, err := c.findRunner(name)
		if err == nil && id != 0 && status == "online" {
			labels, _ := c.labels(id)
			fmt.Fprintf(stdout, "%s is online, labels: %s\n", name, strings.Join(labels, ","))
			fmt.Fprintln(stdout, "a workflow reaches it with runs-on matching ALL of those, case-insensitively")
			return 0
		}
		time.Sleep(sleepStep)
	}
	fmt.Fprintf(stderr, "ghrunner: %s registered but has not come online. `sudo ./svc.sh status` on the machine says why.\n", name)
	return 1
}

// sleepStep is a variable so a test does not wait.
var sleepStep = 2 * time.Second

// sshConfigure runs config.sh over a PTY with the echo off, feeding the token
// on stdin.
//
// -tt forces a terminal even though our own stdin is a pipe, because
// Console.ReadKey wants one. `stty -echo` stops the PTY reflecting the token
// into the session, which is what makes this safe to run under anything that
// records output. The trailing newlines answer any prompt the flags did not
// cover, taking its default rather than hanging on EOF — the first attempt
// here stopped dead at "Enter the name of the runner group".
func (c *client) sshConfigure(host string, cfgArgs []string, token string, stdout, stderr io.Writer) int {
	remote := "stty -echo 2>/dev/null; cd ~/actions-runner && ./config.sh " +
		strings.Join(quoteAll(cfgArgs), " ") + "; rc=$?; stty echo 2>/dev/null; exit $rc"
	cmd := execCommand("ssh", "-tt", "-o", "BatchMode=yes", host, remote)
	cmd.Stdin = strings.NewReader(token + "\n\n\n\n\n")
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(stderr, "ghrunner: config.sh:", err)
		return 1
	}
	return 0
}

func sshRun(host, remote string, stdout, stderr io.Writer) int {
	cmd := execCommand("ssh", "-o", "BatchMode=yes", host, remote)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(stderr, "ghrunner: ssh:", err)
		return 1
	}
	return 0
}

// quoteAll makes each argument one shell word. The values here are names and
// labels rather than anything hostile, and a label with a space in it would
// otherwise become two flags.
func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return out
}

// execCommand is a seam: a test must reach the ssh paths without an ssh.
var execCommand = exec.Command

// redactWriter puts everything on its way out through the redactor.
type redactWriter struct {
	w io.Writer
	r *redact.Redactor
}

func (rw *redactWriter) Write(p []byte) (int, error) {
	if _, err := rw.w.Write(rw.r.Bytes(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// client is the GitHub side.
type client struct {
	token string
	repo  string
	base  string // "" means the real API; a test points it elsewhere
}

func (c *client) url(path string) string {
	base := c.base
	if base == "" {
		base = "https://api.github.com"
	}
	return base + "/repos/" + c.repo + path
}

func (c *client) do(method, path string) ([]byte, int, error) {
	req, err := http.NewRequest(method, c.url(path), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpDo(req)
	if err != nil {
		// Never the request, which carries the Authorization header.
		return nil, 0, fmt.Errorf("%s %s: request failed", method, path)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

var httpDo = func(req *http.Request) (*http.Response, error) { return http.DefaultClient.Do(req) }

func (c *client) registrationToken() (string, error) {
	return c.tokenFrom("/actions/runners/registration-token")
}
func (c *client) removeToken() (string, error) { return c.tokenFrom("/actions/runners/remove-token") }

func (c *client) tokenFrom(path string) (string, error) {
	b, code, err := c.do(http.MethodPost, path)
	if err != nil {
		return "", err
	}
	if code != http.StatusCreated {
		return "", fmt.Errorf("%s: HTTP %d — does the token in %s carry the repo scope?", path, code, ghauth.DefaultFile)
	}
	var v struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &v); err != nil || v.Token == "" {
		return "", errors.New("the API answered without a token")
	}
	return v.Token, nil
}

type runner struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (c *client) runners() ([]runner, error) {
	b, code, err := c.do(http.MethodGet, "/actions/runners?per_page=100")
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("listing runners: HTTP %d", code)
	}
	var v struct {
		Runners []runner `json:"runners"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, errors.New("listing runners: the API answered with something that is not a runner list")
	}
	return v.Runners, nil
}

func (c *client) findRunner(name string) (int, string, error) {
	rs, err := c.runners()
	if err != nil {
		return 0, "", err
	}
	for _, r := range rs {
		if r.Name == name {
			return r.ID, r.Status, nil
		}
	}
	return 0, "", nil
}

func (c *client) labels(id int) ([]string, error) {
	rs, err := c.runners()
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		if r.ID == id {
			out := make([]string, 0, len(r.Labels))
			for _, l := range r.Labels {
				out = append(out, l.Name)
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("runner %d is not in the list any more", id)
}

func (c *client) deleteRunner(id int) error {
	_, code, err := c.do(http.MethodDelete, fmt.Sprintf("/actions/runners/%d", id))
	if err != nil {
		return err
	}
	if code != http.StatusNoContent {
		return fmt.Errorf("removing runner %d: HTTP %d", id, code)
	}
	return nil
}
