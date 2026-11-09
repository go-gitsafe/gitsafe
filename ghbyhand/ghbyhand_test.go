package ghbyhand

import (
	"strings"
	"testing"
)

// TestRefuses covers every act in the table, plus the raw-endpoint forms that
// reach the same acts.
func TestRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, cmd, wantInstead string
	}{
		{"issue comment", "gh issue comment 282 --body-file report.md", "ghissue"},
		{"pr comment", "gh pr comment 443 --body-file note.md", "ghissue"},
		{"issue close", "gh issue close 282", "ghissue"},
		{"issue reopen", "gh issue reopen 282", "ghissue"},
		{"pr merge", "gh pr merge 443 --squash", "ghmerge"},
		{"repo create", "gh repo create go-zpkg/thing --public", "ghnew"},
		// The act that went through by hand three times on 2026-11-08, while
		// this rule allowed every write outside its table.
		{"issue create", "gh issue create --repo go-fde/fde --title t --body-file b.md", "ghissue <owner/repo> --new"},
		{"api issues collection", "gh api repos/go-fde/fde/issues -f title=t -F body=@b.md", "ghissue <owner/repo> --new"},

		// The programs written because this rule asked for them, one row per
		// act, each in the spelling the record shows was typed.
		{"issue edit", "gh issue edit 677 --body-file b.md", "--edit"},
		{"pr edit", "gh pr edit 17 -R go-notebook/playground --body-file b.md", "ghpr"},
		{"api PATCH of a pull request", "gh api -X PATCH repos/go-zpkg/zbk/pulls/443 -F body=@b.md", "ghpr <owner/repo> <number> edit"},
		{"pr close", "gh pr close 77 -R go-fft/fft", "ghpr"},
		{"pr reopen", "gh pr reopen 17 --repo usbarmory/tamago-go", "ghpr"},
		{"pr ready", "gh pr ready 1 -R go-wasmcontainers/relay", "ghpr"},
		{"pr update-branch", "gh pr update-branch 815", "ghpr"},
		{"pr review", "gh pr review 105 --approve --body-file b.md", "ghpr"},
		{"workflow run", "gh workflow run build.yml -f recipes=x", "ghrun <owner/repo> dispatch"},
		{"run rerun", "gh run rerun 41853676310 -R go-widgets/window --failed", "ghrun <owner/repo> rerun"},
		{"run cancel", "gh run cancel 41239533630", "ghrun <owner/repo> cancel"},
		{"release create", "gh release create v0.40.1 --notes-file n.md", "ghrelease"},
		{"release edit", "gh release edit v0.11.0 -R go-llmflow/gateway --notes-file n.md", "ghrel"},
		{"release upload", "gh release upload v0.1.0 -R go-quake1/freepak pak0.pak", "ghrel"},
		{"release delete", "gh release delete v0.10.0 --repo go-widgets/mvvm --yes", "ghrel"},
		{"repo fork", "gh repo fork glauth/ldap --org go-authn --clone=false", "ghrepo"},
		{"repo edit", "gh repo edit go-authn/ldap --description x", "ghrepo"},
		{"repo rename", "gh repo rename jpeg2000 --repo go-images/go-jpeg2000 --yes", "--confirm"},
		{"repo archive", "gh repo archive tannevaled/notify --yes", "--confirm"},
		{"repo delete", "gh repo delete go-ansible/connection --yes", "--confirm"},
		{"repo sync", "gh repo sync tannevaled/purego --source ebitengine/purego", "ghrepo"},
		{"secret set", "gh secret set RENOVATE_TOKEN --repo go-docutils/.github", "secret-set"},

		// The two forms actually typed on 2026-11-08. The second carries no
		// -X at all: a field makes gh POST.
		{"api comments with a field", "gh api repos/go-zpkg/zbk/issues/282/comments -F body=@r.md", "ghissue"},
		{"api comments with a method", "gh api -X POST repos/o/r/issues/1/comments", "ghissue"},
		{"api comments with --method=", "gh api --method=PATCH repos/o/r/issues/1/comments", "ghissue"},
		{"api comments with --input", "gh api repos/o/r/issues/1/comments --input body.json", "ghissue"},
		{"api comments trailing slash", "gh api -X POST repos/o/r/issues/1/comments/", "ghissue"},

		// Shapes that must not hide the act.
		{"env assignment first", "GH_PAGER=cat gh issue comment 9 --body-file b.md", "ghissue"},
		// ⛔ THERE IS NO ESCAPE, so the prefix v0.37.0 advertised is just an
		// assignment the rule steps over. Asserted rather than left implicit:
		// a reader who finds the old doc comment will try this.
		{"the removed escape does not open it", "GITSAFE_ALLOW_RAW_GH=1 gh issue comment 9 --body-file b.md", "ghissue"},
		{"nor in company", "GH_PAGER=cat GITSAFE_ALLOW_RAW_GH=1 gh pr merge 443", "ghmerge"},
		{"--help for another program does not excuse the act", "gh issue comment 9 --body-file b.md && ghissue --help", "ghissue"},
		{"absolute path", "/opt/homebrew/bin/gh issue comment 9 --body-file b.md", "ghissue"},
		{"second statement", "cd /tmp && gh issue comment 9 --body-file b.md", "ghissue"},
		{"inside a pipeline stage", "echo hi | gh issue comment 9 --body-file b.md", "ghissue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := Check(tc.cmd)
			if !f.Found() {
				t.Fatalf("Check(%q) allowed it", tc.cmd)
			}
			if !strings.Contains(f.Instead, tc.wantInstead) {
				t.Errorf("Instead = %q, want it to name %q", f.Instead, tc.wantInstead)
			}
			if f.Missing {
				t.Errorf("a program covers this act, but the finding says none does")
			}
			// A refusal that cannot say what it refused, or what to do, is the
			// kind that gets worked around.
			if f.Why() == "" || f.String() == "" || f.Act == "" {
				t.Errorf("Why=%q String=%q Act=%q — all three must say something",
					f.Why(), f.String(), f.Act)
			}
		})
	}
}

// TestRefusesWhatNoProgramDoes is the hole closed on 2026-11-08: a write with
// no fleet program used to be ALLOWED, so it was typed by hand. It is now
// refused, and the refusal asks for the tool instead of naming one.
func TestRefusesWhatNoProgramDoes(t *testing.T) {
	for _, cmd := range []string{
		// Writes no fleet program does, still (2026-11-09). The ones that
		// stood here first — pr edit, run rerun, release edit, repo delete… —
		// now have programs, and moved to TestRefuses.
		"gh label create bug",
		"gh issue delete 9",
		"gh issue transfer 9 o/other",
		"gh run delete 42480491136",
		"gh secret delete NAME",
		"gh api -X DELETE repos/o/r/git/refs/heads/x",
		"gh api repos/o/r/pulls -f title=t -f head=x -f base=main",
		"gh api rate_limit -F a=b",
		`gh api graphql -f query='mutation { addStar(input:{starrableId:"x"}) { clientMutationId } }'`,
		// A verb this rule has never heard of is a write until listed as a read.
		"gh issue frobnicate 9",
		// The repository flag before the noun does not hide a write.
		"gh -R o/r issue delete 4",
		"gh --repo=o/r run delete 42",
	} {
		f := Check(cmd)
		if !f.Found() {
			t.Errorf("Check(%q) allowed a write no program covers", cmd)
			continue
		}
		if !f.Missing || f.Instead != MissingTool {
			t.Errorf("Check(%q): Missing=%v Instead=%q, want the missing-tool refusal", cmd, f.Missing, f.Instead)
		}
		if !strings.Contains(f.Why(), "no fleet program") {
			t.Errorf("Check(%q): Why = %q, want it to say no program does it", cmd, f.Why())
		}
	}
	// The refusal must ask for the tool, and say where it goes.
	for _, want := range []string{"ask for a program", "go-fleettools/fleettools", "Do not type it by hand"} {
		if !strings.Contains(MissingTool, want) {
			t.Errorf("MissingTool does not say %q", want)
		}
	}
}

// TestAllows is the half that decides whether this rule survives contact.
//
// ⛔ A GUARD'S FALSE POSITIVES ARE WHAT GET IT DELETED, and this fleet has the
// record: fifteen wrong refusals from an earlier rule. Every line here is a
// command that was actually used in the session this rule came from.
func TestAllows(t *testing.T) {
	for _, cmd := range []string{
		// Reads.
		"gh issue list --state open",
		"gh issue view 282 --json title,body",
		"gh pr list --state open",
		"gh pr view 443 --json body",
		"gh pr checks 443 --watch --interval 30",
		"gh run watch 42480491136 --exit-status",
		"gh run view --job 128727470929 --log-failed",
		"gh release view v0.72.0 --json assets",
		"gh api rate_limit --jq .resources.core",
		"gh api repos/go-zpkg/zbk/commits/424a168/check-runs",

		// A GET of a comment collection is a read: no method, no field.
		"gh api repos/go-zpkg/zbk/issues/282/comments",

		// How a pull request is opened here — and the form literaltick's own
		// advice recommends.
		"gh pr create --title t --body-file b.md",

		// Reads that the old table never had to list, and that the read list
		// now must: each one is a false refusal if it is missing.
		"gh issue status",
		"gh pr diff 443",
		"gh pr status",
		"gh pr checkout 443",
		"gh run list --branch main --status failure",
		"gh run download 42480491136 -n logs",
		"gh repo view go-fde/fde --json name",
		"gh repo list go-fde --limit 100 --json name,description",
		"gh repo clone go-fde/fde ~/Documents/VCS/GIT/localhost/go-fde-fde",
		"gh release list --repo go-fde/fde",
		"gh release download v0.1.0 -p '*.tar.gz'",
		"gh workflow list",
		"gh search issues veracrypt --owner go-fde",
		"gh search code --owner go-fde Detect",
		"gh label list",
		"gh secret list",
		"gh variable get NAME",
		"gh ruleset check main",
		"gh auth token",
		"gh status",
		"gh --version",
		"gh issue",
		// A repository flag BEFORE the noun: 47 runs of the first form in the
		// record, which guardscan found refused before this shipped.
		"gh -R go-webengine/engine pr checks 282 --watch",
		"gh --repo go-pkgx/pkgx pr view 89 --json statusCheckRollup",
		"gh --repo=go-pkgx/pkgx release list",
		"gh -Rgo-pkgx/pkgx run list",
		"gh -R",
		"gh -R o/r",
		// Local configuration, not GitHub state.
		"gh auth setup-git",
		"gh repo set-default go-fde/fde",
		"gh ssh-key list",
		"gh gpg-key list",
		// A GraphQL QUERY is POSTed by gh, and is still a read.
		`gh api graphql -f query='query { viewer { login } }'`,
		"gh api graphql -F owner=o -f query=@q.graphql",
		// Explicit GET with a field: gh sends the fields as a query string.
		"gh api -X GET search/issues -f q=veracrypt",

		// The words, not the act.
		`git commit -m "gh issue comment was refused twice"`,
		`echo 'gh pr merge 443' > notes.txt`,

		// ⛔ ASKING WHAT A COMMAND DOES IS NOT DOING IT. This rule refused all
		// four of these until hookcheck said so, on a row that was already in
		// the corpus for the sibling rules.
		"gh pr merge --help",
		"gh issue comment --help",
		"gh issue close -h",
		"gh repo create --help",

		// Other programs whose names contain the verbs.
		"ghissue go-zpkg/zbk 282 --body-file r.md",
		"ghmerge 443",
		"ghrelease 443 v0.72.0",
		"gitpush origin branch",

		// A bare gh, and a two-word gh that is not in the table.
		"gh",
		"gh auth status",
		"gh version",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("Check(%q) refused it: %s / %s", cmd, f.Why(), f.Instead)
		}
	}
}

// TestAQuotedHeredocIsNotTheAct: prose about this rule, written into a file,
// must not trip it.
func TestAQuotedHeredocIsNotTheAct(t *testing.T) {
	cmd := "cat <<'EOT' > note.md\nDo not run gh issue comment 282 by hand.\nEOT"
	if f := Check(cmd); f.Found() {
		t.Errorf("a quoted heredoc was refused: %s", f.String())
	}
}

// TestZeroValueSaysNothing: the empty finding must not pretend to be one.
func TestZeroValueSaysNothing(t *testing.T) {
	var f Finding
	if f.Found() {
		t.Error("the zero value reports a refusal")
	}
	if f.String() != "" {
		t.Errorf("String() on nothing = %q", f.String())
	}
}

// TestWritesAPI covers the method and field spellings one by one, including the
// ones that must NOT count as a write.
func TestWritesAPI(t *testing.T) {
	for _, tc := range []struct {
		words []string
		want  bool
	}{
		{[]string{"gh", "api", "p", "-X", "POST"}, true},
		{[]string{"gh", "api", "p", "-X", "patch"}, true}, // case folded
		{[]string{"gh", "api", "p", "--method", "DELETE"}, true},
		{[]string{"gh", "api", "p", "--method=PUT"}, true},
		{[]string{"gh", "api", "p", "-f", "a=b"}, true},
		{[]string{"gh", "api", "p", "--field=a=b"}, true},
		{[]string{"gh", "api", "p", "--raw-field", "a=b"}, true},
		{[]string{"gh", "api", "p", "--raw-field=a=b"}, true},
		{[]string{"gh", "api", "p", "--input=b.json"}, true},

		{[]string{"gh", "api", "p"}, false},
		{[]string{"gh", "api", "p", "-X", "GET"}, false},
		{[]string{"gh", "api", "p", "--method=HEAD"}, false},
		{[]string{"gh", "api", "p", "--jq", ".x"}, false},
		// An explicit GET wins over fields: gh sends them as a query string.
		{[]string{"gh", "api", "-X", "GET", "p", "-f", "q=x"}, false},
		{[]string{"gh", "api", "p", "-f", "q=x", "--method=GET"}, false},
		{[]string{"gh", "api", "p", "-f", "a=b", "-X", "POST"}, true},
		// -X as the last word: nothing follows it, and reading past the end
		// would be a panic rather than a refusal.
		{[]string{"gh", "api", "p", "-X"}, false},
		{[]string{"gh", "api", "p", "--method"}, false},
	} {
		if got := writesAPI(tc.words); got != tc.want {
			t.Errorf("writesAPI(%v) = %v, want %v", tc.words, got, tc.want)
		}
	}
}

// TestIsCommentsPath keeps the endpoint rule narrow on purpose.
func TestIsCommentsPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"repos/o/r/issues/1/comments", true},
		{"repos/o/r/issues/1/comments/", true},
		{"/repos/o/r/issues/282/comments", true},

		{"repos/o/r/issues/1", false},
		{"repos/o/r/issues", false},
		{"repos/o/r/pulls/1/comments", false}, // a review comment: no program yet
		{"repos/o/r/issues/comments/1", false},
		{"repos/o/r/releases", false},
		{"rate_limit", false},
	} {
		if got := isCommentsPath(tc.path); got != tc.want {
			t.Errorf("isCommentsPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestIsAssignment: a leading VAR=value is dropped, and a word that merely
// holds an `=` is not an assignment.
func TestIsAssignment(t *testing.T) {
	for _, tc := range []struct {
		word string
		want bool
	}{
		{"GH_PAGER=cat", true},
		{"A=", true},
		{"=x", false},    // no name: this is a command word
		{"a/b=c", false}, // a path, not a name
		{`"a=b"`, false}, // quoted: not an assignment the shell applies
		{"plain", false},
	} {
		if got := isAssignment(tc.word); got != tc.want {
			t.Errorf("isAssignment(%q) = %v, want %v", tc.word, got, tc.want)
		}
	}
}

// TestIsGH recognises the CLI by basename, through quotes and paths.
func TestIsGH(t *testing.T) {
	for _, tc := range []struct {
		word string
		want bool
	}{
		{"gh", true},
		{"/usr/local/bin/gh", true},
		{`"gh"`, true},
		{"'gh'", true},
		{"ghissue", false},
		{"ghmerge", false},
		{"/usr/local/bin/ghissue", false},
		{"github", false},
	} {
		if got := isGH(tc.word); got != tc.want {
			t.Errorf("isGH(%q) = %v, want %v", tc.word, got, tc.want)
		}
	}
}

// TestShortTruncates so a refusal can point at a long statement without
// printing all of it.
func TestShort(t *testing.T) {
	if got := short("  gh   issue\tcomment  1 "); got != "gh issue comment 1" {
		t.Errorf("short collapsed to %q", got)
	}
	long := "gh issue comment 282 --body-file " + strings.Repeat("x", 100)
	got := short(long)
	if !strings.HasSuffix(got, "…") || len(got) > 80 {
		t.Errorf("short(long) = %q (len %d)", got, len(got))
	}
}

// TestIsPullPath keeps "edit a pull request" to one pull request.
func TestIsPullPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"repos/o/r/pulls/443", true},
		{"/repos/o/r/pulls/443/", true},
		{"repos/o/r/pulls", false},
		{"repos/o/r/pulls/443/reviews", false},
		{"repos/o/r/issues/443", false},
		{"", false},
	} {
		if got := isPullPath(tc.path); got != tc.want {
			t.Errorf("isPullPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestIsIssuesCollection keeps "open an issue" to the one endpoint that does it.
func TestIsIssuesCollection(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"repos/o/r/issues", true},
		{"/repos/o/r/issues/", true},
		{"repos/o/r/issues/1", false},
		{"repos/o/r/issues/1/comments", false},
		{"repos/o/r/pulls", false},
		{"orgs/o/issues", false},
		{"", false},
	} {
		if got := isIssuesCollection(tc.path); got != tc.want {
			t.Errorf("isIssuesCollection(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestCheckAPIWithoutAPath: `gh api` with no path-looking argument and no
// write must not panic and must not refuse.
func TestCheckAPIWithoutAPath(t *testing.T) {
	for _, cmd := range []string{
		"gh api",
		"gh api --jq .x",
		"gh api rate_limit",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("Check(%q) refused it: %s", cmd, f.Instead)
		}
	}
}
