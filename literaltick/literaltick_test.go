package literaltick

import "testing"

// The refusals. Each one is a command the shell would run something inside.
func TestRefused(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		double    bool
	}{
		// ⛔ THE INCIDENT, three times in one session. The shell ran
		// `brandcheck -org`, spliced its usage error into the message, and
		// agentsync answered "told … 0" because storing a string succeeds.
		{"the handover that was mangled",
			"agentsync tell peer \"the eighth was `brandcheck -org` in brand-complete.yml\"", true},
		{"a second command in the same message",
			"agentsync tell peer \"and `vulnscan -selftest` twice\"", true},
		{"a build line quoted for prose",
			"agentsync tell peer \"`go build -o /tmp/vulnscan ./cmd/vulnscan` is not a call\"", true},

		{"bare, outside any quotes",
			"echo `date`", false},
		{"a commit message typed inline",
			"git commit -m \"ci: move the pin, see `gopin --help`\"", true},
		{"a pull request body typed inline",
			"gh pr create --title x --body \"it fixes `vulngate -go`\"", true},
		{"nested in a longer double-quoted sentence",
			"foo --note \"first, then `x`, then last\"", true},
		{"two of them, the first is enough",
			"echo \"`a` and `b`\"", true},
		{"a backslash that escapes another backslash, not the backtick",
			`echo "path\\` + "`x`" + `"`, true},

		// ⛔ THIS ONE WAS IN THE ALLOW TABLE UNTIL THE SHELL WAS ASKED. A single
		// quote is NOT special inside double quotes — it is an apostrophe — so
		// the backticks still substitute. Measured rather than reasoned:
		//
		//	sh -c `echo "see '"'"'`echo RAN`'"'"'"`   ->   see 'RAN'
		//
		// The rule was right and the test case was wrong, which is the only way
		// round that costs nothing to find.
		{"single quotes inside double quotes are apostrophes, not quoting",
			"foo --note \"see '`x`'\"", true},
	} {
		f := Check(tc.cmd)
		if !f.Found() {
			t.Errorf("%s: allowed\n  %s", tc.name, tc.cmd)
			continue
		}
		if f.InDoubleQuotes != tc.double {
			t.Errorf("%s: InDoubleQuotes = %v, want %v", tc.name, f.InDoubleQuotes, tc.double)
		}
		if f.Why() == "" || f.String() == "" {
			t.Errorf("%s: a refusal must say what it matched", tc.name)
		}
	}
}

// ⛔ THE LONGER HALF, ON PURPOSE. A guard that refuses what the shell does not
// run gets worked around by lunchtime, and this fleet has already paid for
// fifteen false positives from this binary. Every line here must pass.
func TestAllowed(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"single quotes stop the shell dead",
			"agentsync tell peer 'the eighth was `brandcheck -org` in brand-complete.yml'"},
		{"prose in a file, which is the advice",
			"git commit -F /tmp/msg.txt"},
		{"a body from a file",
			"gh pr create --title x --body-file /tmp/b.md"},
		{"the modern substitution form",
			"v=$(git rev-parse HEAD)"},
		{"the modern form inside double quotes",
			"vulngate --go \"$(go env GOVERSION)\" /tmp/vuln.json"},
		{"an escaped backtick is literal",
			`echo "cost \` + "`" + `x\` + "`" + ` dollars"`},
		{"a grep pattern in single quotes",
			"grep -n 'func `x`' file.go"},
		{"no backtick at all",
			"go test ./... > /tmp/t.txt 2>&1"},
		{"an apostrophe is not a backtick",
			"echo \"the command's own exit status\""},
		{"a quoted heredoc is not expanded",
			"cat <<'EOF'\nuse `x` here\nEOF"},
	} {
		if f := Check(tc.cmd); f.Found() {
			t.Errorf("%s: refused at %d (%s)\n  %s", tc.name, f.Off, f.Context, tc.cmd)
		}
	}
}

// ⛔ The planted positive for the DETECTOR itself, not for the rule: if Check
// ever stops finding the live case, every table above goes quietly green.
func TestTheDetectorCanFire(t *testing.T) {
	const live = "agentsync tell peer \"and `vulnscan -selftest` twice\""
	f := Check(live)
	if !f.Found() {
		t.Fatal("the detector no longer sees the command that caused this package")
	}
	if f.Off != 27 {
		// Not load-bearing, but a wrong offset means the window points elsewhere.
		t.Logf("offset %d (context %q)", f.Off, f.Context)
	}
	if !f.InDoubleQuotes {
		t.Error("the double-quote case is the one that surprises people; it must be reported")
	}
	// And the advice has to name the two ways out, or it is a refusal with no door.
	for _, want := range []string{"SINGLE", "file", "$(", Escape} {
		if !contains(Advice, want) {
			t.Errorf("the advice does not mention %q", want)
		}
	}
}

// ⛔ THE ESCAPE IS READ FROM THE COMMAND, NOT THE ENVIRONMENT, and the first
// version of this package had it the other way round. guard-bash runs BEFORE
// any shell, so `GITSAFE_ALLOW_BACKTICK=1 cmd` has not been assigned to
// anything when the rule reads it: an os.Getenv test could never fire and the
// documented escape would not exist. [ghbyhand] had the identical defect, fixed
// in v0.37.1, and its comment in cmd/guard-bash/main.go is what caught this one
// during a rebase.
//
// ⛔ The corpus row for the escape passed the whole time — against an installed
// binary that did not carry this rule at all. A row can be green because the
// subject is absent.
func TestTheEscapeIsReadFromTheCommandText(t *testing.T) {
	const bad = "echo `date`"
	if !Check(bad).Found() {
		t.Fatal("the unescaped form must be refused, or this test proves nothing")
	}
	for _, c := range []struct {
		name, cmd string
		want      bool // want a refusal
	}{
		{"the escape in front, as it is typed", "GITSAFE_ALLOW_BACKTICK=1 " + bad, false},
		{"another assignment first, then the escape", "FOO=bar GITSAFE_ALLOW_BACKTICK=1 " + bad, false},
		{"explicitly off", "GITSAFE_ALLOW_BACKTICK=0 " + bad, true},
		{"empty is not an escape", "GITSAFE_ALLOW_BACKTICK= " + bad, true},
		// ⛔ Not an escape once the command has started: a word that happens to
		// look like the assignment, somewhere in the arguments, must not disarm
		// the rule for the rest of the line.
		{"after the command, it is an argument", "echo GITSAFE_ALLOW_BACKTICK=1 " + "`date`", true},
		{"another rule's escape is not this one", "GITSAFE_ALLOW_VOLATILE=1 " + bad, true},
	} {
		if got := Check(c.cmd).Found(); got != c.want {
			t.Errorf("%s: refused = %v, want %v\n  %s", c.name, got, c.want, c.cmd)
		}
	}

	// And the environment has no say, in either direction.
	t.Setenv(Escape, "1")
	if !Check(bad).Found() {
		t.Error("an environment variable disarmed a rule that runs before any shell")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
