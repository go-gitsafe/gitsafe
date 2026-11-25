package ghbyhand

import (
	"strings"
	"testing"
)

// Every spelling of GitHub code search is refused, naming git fetch + git grep.
func TestCodeSearchIsRefusedInEverySpelling(t *testing.T) {
	cases := []struct{ name, cmd string }{
		{"gh search code", `gh search code A11yElements --repo claimward/claimward-vpn-app-linux`},
		{"gh search code quoted", `gh search code "A11yElements repo:o/r"`},
		{"gh search code global repo", `gh -R o/r search code foo`},
		{"gh by absolute path", `/opt/homebrew/bin/gh search code foo`},
		{"env assignment first", `GH_TOKEN=x gh search code foo`},
		{"api -f q", `gh api search/code -f q="A11yElements repo:claimward/claimward-vpn-app-linux"`},
		{"api leading slash", `gh api /search/code -f q=foo`},
		{"api -X GET", `gh api -X GET search/code -f q=foo`},
		{"api --method GET", `gh api --method GET search/code -f q=foo`},
		{"api flags before path", `gh api -H "Accept: application/vnd.github+json" -f q=repo:o/r search/code`},
		{"api query in URL", `gh api "search/code?q=foo+repo:o/r"`},
		{"api query in URL slash", `gh api '/search/code?q=A11yElements'`},
		{"api trailing slash", `gh api search/code/ -f q=foo`},
		{"api full URL", `gh api https://api.github.com/search/code?q=foo`},
		{"api with jq", `gh api search/code -f q=foo --jq .total_count`},
		{"curl", `curl -s "https://api.github.com/search/code?q=foo+repo:o/r"`},
		{"curl header", `curl -H "Accept: application/vnd.github+json" https://api.github.com/search/code?q=foo`},
		{"curl --url=", `curl --url=https://api.github.com/search/code?q=foo`},
		{"curl -G", `curl -G https://api.github.com/search/code --data-urlencode "q=foo"`},
		{"wget", `wget -qO- "https://api.github.com/search/code?q=foo"`},
		{"curl by path", `/usr/bin/curl https://api.github.com/search/code?q=x`},
		{"after &&", `cd x && gh search code foo`},
		{"in a pipe stage", `echo hi; gh api search/code -f q=foo`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Check(tc.cmd)
			if !f.Found() {
				t.Fatalf("Check(%q) allowed GitHub code search", tc.cmd)
			}
			if !f.CodeSearch || f.Missing {
				t.Errorf("Check(%q): CodeSearch=%v Missing=%v, want the code-search refusal", tc.cmd, f.CodeSearch, f.Missing)
			}
			for _, want := range []string{"git fetch", "git grep -n", "origin/main", "~/Documents/VCS/GIT/localhost", "shallow"} {
				if !strings.Contains(f.Instead, want) {
					t.Errorf("Instead does not mention %q:\n%s", want, f.Instead)
				}
			}
			if f.Why() != "it would "+codeSearchAct || strings.Contains(f.Why(), "by hand") || f.String() == "" {
				t.Errorf("Why = %q, String = %q", f.Why(), f.String())
			}
		})
	}
}

// endOfWord's edges: past the end, an empty word, a lone quote, an unclosed quote.
func TestEndOfWord(t *testing.T) {
	for _, tc := range []struct {
		words   []string
		i, want int
	}{
		{[]string{"a"}, 5, 0},
		{[]string{"a"}, 1, 0},
		{[]string{"a", "b"}, 2, 1},
		{[]string{"a", ""}, 1, 1},
		{[]string{"a", `"x`, `y"`, "z"}, 1, 2},
		{[]string{"a", `"x"`, "z"}, 1, 1},
		{[]string{"a", `"`, `y"`}, 1, 2},
		{[]string{"a", `"x`, "y"}, 1, 2},
		{[]string{"a", "x"}, 1, 1},
	} {
		if got := endOfWord(tc.words, tc.i); got != tc.want {
			t.Errorf("endOfWord(%q, %d) = %d, want %d", tc.words, tc.i, got, tc.want)
		}
	}
	if codeSearchFetcher(nil) || codeSearchFetcher([]string{"curl"}) || apiEndpoint([]string{"gh", "api"}) != "" {
		t.Error("a bare curl / bare gh api matched")
	}
}

// The siblings that answer from the tracker, and the words about the rule, stay
// allowed: a refusal wider than the defect is how a guard earns false positives.
func TestCodeSearchNegativeControls(t *testing.T) {
	for _, cmd := range []string{
		`gh search issues foo --repo o/r`,
		`gh search prs foo`,
		`gh search repos foo`,
		`gh search commits foo`,
		`gh api repos/x/y/contents/README.md`,
		`gh api repos/x/y/git/trees/main?recursive=1`,
		`gh api search/issues`,
		`gh api "search/issues?q=foo+repo:o/r"`,
		`gh api -X GET search/issues -f q="foo repo:o/r"`,
		`gh api -X GET search/issues -f q=foo`,
		`gh api --method GET search/repositories -f q=foo`,
		`gh api -X GET -f q=repo:o/r search/issues`,
		`gh api -H "Accept: application/vnd.github+json" "search/issues?q=x"`,
		`gh api search/codex`,
		`gh api repos/o/search/code`,
		`gh search code --help`,
		`curl https://api.github.com/search/issues?q=foo`,
		`curl https://api.github.com/repos/o/r/contents/x`,
		`curl https://example.com/search/code`,
		`wget https://example.com/api.github.com`,
		`git grep -n A11yElements origin/main`,
		`git commit -m "refuse gh search code and gh api search/code"`,
		`echo gh search code`,
		`git fetch origin`,
		`echo https://api.github.com/search/code?q=x`,
		`git log --grep=https://api.github.com/search/code`,
		`ls https://api.github.com/search/code`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("Check(%q) refused it: %s / %s", cmd, f.Why(), f.Instead)
		}
	}
}
