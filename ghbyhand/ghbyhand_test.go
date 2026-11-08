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

		// The two forms actually typed on 2026-11-08. The second carries no
		// -X at all: a field makes gh POST.
		{"api comments with a field", "gh api repos/go-zpkg/zbk/issues/282/comments -F body=@r.md", "ghissue"},
		{"api comments with a method", "gh api -X POST repos/o/r/issues/1/comments", "ghissue"},
		{"api comments with --method=", "gh api --method=PATCH repos/o/r/issues/1/comments", "ghissue"},
		{"api comments with --input", "gh api repos/o/r/issues/1/comments --input body.json", "ghissue"},
		{"api comments trailing slash", "gh api -X POST repos/o/r/issues/1/comments/", "ghissue"},

		// Shapes that must not hide the act.
		{"env assignment first", "GH_PAGER=cat gh issue comment 9 --body-file b.md", "ghissue"},
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
			// A refusal that cannot say what it refused, or what to do, is the
			// kind that gets worked around.
			if f.Why() == "" || f.String() == "" || f.Act == "" {
				t.Errorf("Why=%q String=%q Act=%q — all three must say something",
					f.Why(), f.String(), f.Act)
			}
		})
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

		// No fleet program edits a pull request body yet, so refusing this
		// would be a refusal with no answer.
		"gh api -X PATCH repos/go-zpkg/zbk/pulls/443 -F body=@b.md",

		// The words, not the act.
		`git commit -m "gh issue comment was refused twice"`,
		`echo 'gh pr merge 443' > notes.txt`,

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

// TestCheckAPIWithoutAPath: `gh api` with no path-looking argument must not
// panic and must not refuse.
func TestCheckAPIWithoutAPath(t *testing.T) {
	for _, cmd := range []string{
		"gh api",
		"gh api --jq .x",
		"gh api rate_limit -F a=b", // a write, but not to a comments path
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("Check(%q) refused it: %s", cmd, f.Instead)
		}
	}
}
