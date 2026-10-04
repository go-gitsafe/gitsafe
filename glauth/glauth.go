// Package glauth is ghauth for GitLab: how to read a GitLab token without ever
// putting it somewhere it could be echoed, how to ask a GitLab instance about
// it, and what its scopes allow.
//
// It exists because the first look at a GitLab token on this machine leaked it.
// The token file was expected to hold "PRIVATE-TOKEN: <token>", it held the bare
// token, and a format check that printed "the header name" printed the token.
// So the rules live here once: a token file may hold either shape, nothing in
// this package ever formats a token into a string a caller could print, and the
// only place the token goes is the PRIVATE-TOKEN header of a request.
package glauth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// HeaderName is the header GitLab reads a personal, project or group access
// token from.
const HeaderName = "PRIVATE-TOKEN"

// TokenPath chooses the token file for host: the one named explicitly, else a
// per-instance file ~/.gitlab-token-<host>, else ~/.gitlab-token. A machine
// talking to several GitLab instances keeps one token per instance, and a token
// sent to the wrong instance is a token handed to a stranger.
func TokenPath(home, host, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if host != "" {
		perHost := filepath.Join(home, ".gitlab-token-"+host)
		if _, err := os.Stat(perHost); err == nil {
			return perHost
		}
	}
	return filepath.Join(home, ".gitlab-token")
}

// Read returns the token in path.
//
// The file may hold the bare token or a "PRIVATE-TOKEN: <token>" line: both
// were asked for at one time or another, and the difference is what leaked one.
// No error ever contains a byte of the file, so a failure cannot show what a
// success would have protected.
func Read(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	switch len(lines) {
	case 0:
		return "", fmt.Errorf("%s is empty", path)
	case 1:
	default:
		return "", fmt.Errorf("%s holds %d non-empty lines; expected one token", path, len(lines))
	}
	tok := lines[0]
	if name, value, ok := strings.Cut(tok, ":"); ok && strings.EqualFold(strings.TrimSpace(name), HeaderName) {
		tok = strings.TrimSpace(value)
	}
	if tok == "" || strings.ContainsAny(tok, " \t") {
		return "", fmt.Errorf("%s does not hold a token: expected the token alone, or %q followed by it", path, HeaderName+":")
	}
	return tok, nil
}

// Client asks one GitLab instance's REST API, with a token that only ever goes
// into a request header.
type Client struct {
	// Base is the API root, e.g. https://gitlab.example.org/api/v4.
	Base  string
	token string
	HTTP  *http.Client
}

// New returns a client for host (a name such as plmlab.math.cnrs.fr, or a full
// https URL).
func New(host, token string) *Client {
	base := strings.TrimSuffix(host, "/")
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return &Client{Base: base + "/api/v4", token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Token returns the secret, for a caller that must mask it in text it prints
// (redact.New(c.Token())). It is the only way out of the client.
func (c *Client) Token() string { return c.token }

// Get fetches path (relative to the API root, query included) and returns the
// body, or a *Refusal saying which refusal it was.
func (c *Client) Get(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(HeaderName, c.token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// A transport error can quote the URL, never a header: the token is not
		// in the URL, so nothing here can carry it.
		return nil, fmt.Errorf("asking %s: %w", c.Base, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("reading the answer of %s: %w", c.Base, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Refusal{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After"), Path: strings.SplitN(path, "?", 2)[0]}
	}
	return body, nil
}

// GetJSON is Get decoded into v.
func (c *Client) GetJSON(path string, v any) error {
	b, err := c.Get(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("reading the answer to %s: %w", strings.SplitN(path, "?", 2)[0], err)
	}
	return nil
}

// Refusal is a non-200 answer, named for what it means rather than guessed at.
type Refusal struct {
	Status     int
	RetryAfter string
	Path       string
}

func (r *Refusal) Error() string {
	switch r.Status {
	case http.StatusUnauthorized:
		return fmt.Sprintf("%s: 401, the token was refused: revoked, expired, mistyped, or issued by another GitLab instance", r.Path)
	case http.StatusForbidden:
		return fmt.Sprintf("%s: 403, the token was accepted but may not do this: a scope is missing (reads need read_api, writes need api) or the account's role in the project is too low", r.Path)
	case http.StatusNotFound:
		return fmt.Sprintf("%s: 404, not found — or not visible to the token's account: GitLab answers 404, not 403, for a private project you cannot see", r.Path)
	case http.StatusTooManyRequests:
		if r.RetryAfter != "" {
			return fmt.Sprintf("%s: 429, rate limited; retry after %s s", r.Path, r.RetryAfter)
		}
		return fmt.Sprintf("%s: 429, rate limited", r.Path)
	}
	return fmt.Sprintf("%s: HTTP %d", r.Path, r.Status)
}

// Token is what GitLab says about the token it was sent.
type Token struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at"`
	Active    bool     `json:"active"`
	Revoked   bool     `json:"revoked"`
	UserID    int      `json:"user_id"`
}

// Self describes the token the client holds. Any token may ask this about
// itself, whatever its scopes.
func (c *Client) Self() (Token, error) {
	var t Token
	err := c.GetJSON("/personal_access_tokens/self", &t)
	return t, err
}

// Username is the account the token acts as; it needs read_user, read_api or
// api, and an empty answer is not an error.
func (c *Client) Username() string {
	var u struct {
		Username string `json:"username"`
	}
	if err := c.GetJSON("/user", &u); err != nil {
		return ""
	}
	return u.Username
}

// Implies maps a scope onto the scopes it CONTAINS.
//
// Measured in GitLab's source, not assumed: api passes every API scope check
// while read_api passes only GET and HEAD (lib/api/api.rb:
// allow_access_with_scope :read_api, if: request.get? || request.head?), and
// write_repository grants download_code with push_code (lib/gitlab/auth.rb,
// abilities_for_scopes). write_registry does NOT contain read_registry — it
// grants create_container_image only — so it has no entry: a list of
// containments copied from GitHub's would have said otherwise.
var Implies = map[string][]string{
	"api":              {"read_api"},
	"write_repository": {"read_repository"},
}

// Missing returns the scopes of want that have does not grant, directly or
// through Implies.
func Missing(have, want []string) []string {
	granted := map[string]bool{}
	for _, s := range have {
		granted[s] = true
		for _, inner := range Implies[s] {
			granted[inner] = true
		}
	}
	var out []string
	for _, w := range want {
		if !granted[w] {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

// Remote is a GitLab project named by a git remote URL.
type Remote struct {
	Host    string // e.g. plmlab.math.cnrs.fr
	Project string // e.g. resinfo/gt/gt-cloud/docs
}

// ParseRemote reads host and project path from a git remote URL in any of the
// usual forms: git@host:group/project.git, ssh://git@host[:port]/group/project.git,
// https://host/group/project.git. It returns false for anything else, and never
// for a URL carrying a password: that URL must be fixed, not used.
func ParseRemote(raw string) (Remote, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Remote{}, false
	}
	var host, path string
	if !strings.Contains(raw, "://") {
		// scp-like: [user@]host:path
		at := strings.LastIndex(raw, "@")
		rest := raw[at+1:]
		h, p, ok := strings.Cut(rest, ":")
		if !ok || h == "" || strings.HasPrefix(p, "/") && strings.HasPrefix(p, "//") {
			return Remote{}, false
		}
		host, path = h, p
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return Remote{}, false
		}
		if _, hasPass := u.User.Password(); hasPass {
			return Remote{}, false
		}
		host, path = u.Hostname(), u.Path
	}
	path = strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if host == "" || !strings.Contains(path, "/") {
		return Remote{}, false
	}
	return Remote{Host: host, Project: path}, true
}

// ProjectID is the project path in the form the API accepts in place of a
// numeric id.
func ProjectID(project string) string { return url.PathEscape(project) }
