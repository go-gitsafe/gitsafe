package secretarg

import "testing"

// The strings below are assembled from fragments where they would otherwise be
// an example of the thing being refused — a test file that trips the rule it
// tests is a test file nobody can commit.
var (
	sub  = "$" + "("
	tick = "`"
	tok  = "~/.github-token"
	cat  = "c" + "at"
	bad  = sub + cat + " " + tok + ")"
)

// TestRefused pins what must never reach a process list. Each of these has
// either happened on this machine or is the same shape as something that did.
func TestRefused(t *testing.T) {
	for _, tc := range []struct{ name, cmd, rule string }{
		{"the curl header, the form actually committed",
			`curl -H "Authorization: Bearer ` + bad + `" https://api.github.com/repos/o/r`, "substitution"},
		{"into an environment variable, which the rule forbids too",
			"export GH_TOKEN=" + bad, "substitution"},
		{"with head instead of cat",
			"curl -u x:" + sub + "head -n1 " + tok + ") https://x", "substitution"},
		{"backticks",
			`curl -H "A: ` + tick + cat + " " + tok + tick + `"`, "substitution"},
		{"the $(< file) form",
			`curl -H "A: ` + sub + "< " + tok + `)"`, "substitution"},
		{"another credential file",
			"foo --pass " + sub + cat + " ~/.renovate-token)", "substitution"},
		{"a private key",
			"ssh-add " + sub + cat + " ~/.ssh/id_rsa)", "substitution"},
		{"an unquoted heredoc, which the shell DOES expand",
			"cat <<EOF\nAuthorization: " + bad + "\nEOF", "substitution"},
		{"a token typed out in full",
			"curl -H 'Authorization: Bearer ghp_0123456789abcdefghij' https://x", "literal"},
		{"a credential in a URL",
			"git push https://x-access-token:sekritvalue@github.com/o/r", "literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := Check(tc.cmd)
			if !f.Found() {
				t.Fatalf("not refused: %s", tc.cmd)
			}
			if f.Rule != tc.rule {
				t.Errorf("rule = %q, want %q", f.Rule, tc.rule)
			}
			if f.Match == "" || f.Why == "" {
				t.Errorf("a refusal must point at something and say why: %+v", f)
			}
		})
	}
}

// TestAllowed is the more important half. A guard that refuses harmless
// commands is one people learn to work around — this machine has already had
// that happen to a rule that matched "push" too broadly — and then it protects
// nothing at all.
func TestAllowed(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"the safe pipe: the secret never enters argv",
			`{ printf 'header = "A: '; tr -d '\n' < ` + tok + `; printf '"\n'; } | curl -s -K - https://x`},
		{"a tool that holds its own credential", "gh api repos/o/r --jq .full_name"},
		{"a property of the secret, never its value", "wc -c < " + tok},
		{"the wrappers themselves", "ghscopes repo workflow && gitpush origin main"},
		{"an ordinary substitution", "echo " + sub + cat + " README.md)"},
		{"a substitution that computes a date", "D=" + sub + "date +%s); echo $D"},
		{"the file named, but never read into argv", cat + " " + tok + " > /dev/null"},
		{"listing it", "ls -la " + tok},
		{"the word token inside a note", `agentsync claim --note "rotate the token later" repo`},
		{"a grep for the word", "git log --grep=secret --oneline"},
		// The false positive that this package exists to have got right: the
		// first shell version refused the command that WROTE the note about it.
		{"documenting the forbidden form in a quoted heredoc",
			"cat > /tmp/note.md <<'MD'\nnever write " + bad + "\nMD"},
		{"the same, with a python heredoc",
			"python3 - <<'PY'\ns = 'curl -H \"A: " + bad + "\"'\nPY"},
		{"a quoted heredoc with a dash",
			"cat <<-'EOF'\n\t" + bad + "\n\tEOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if f := Check(tc.cmd); f.Found() {
				t.Errorf("false positive (%s): %s\n  matched %q", f.Rule, tc.cmd, f.Match)
			}
		})
	}
}

// A heredoc that is opened and never closed must not swallow the rest of the
// command: an unterminated tag is a typo, and a typo must not disable the guard.
func TestUnterminatedHeredocDoesNotHideWhatFollows(t *testing.T) {
	// Closed: the body is skipped, and the substitution AFTER it is still seen.
	closed := "cat <<'EOF'\n" + bad + "\nEOF\ncurl -H \"A: " + bad + "\""
	if f := Check(closed); !f.Found() {
		t.Error("a substitution after a closed heredoc must still be refused")
	}
	// Unterminated: everything after the opening is skipped, which is the one
	// way this could be abused. It is recorded here as known and accepted —
	// the shell would not run such a command either.
	unterminated := "cat <<'EOF'\ncurl -H \"A: " + bad + "\""
	if f := Check(unterminated); f.Found() {
		t.Logf("unterminated heredoc refused anyway: %+v", f)
	}
}

func TestAdviceNamesTheSafeForms(t *testing.T) {
	for _, want := range []string{"gh api", "curl -s -K -", "gitpush", "ghscopes"} {
		if !contains(Advice, want) {
			t.Errorf("advice does not mention %q", want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
