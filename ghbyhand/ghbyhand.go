// Package ghbyhand answers one question: does this command change state on
// GitHub by hand, when the fleet already has a program that does it?
//
// # Why it exists
//
// Measured on 2026-11-08, in one session, posting one comment to one issue:
//
//	gh issue comment 282 --body-file report.md            → refused
//	gh api repos/o/r/issues/282/comments -F body=@report.md → refused
//	ghissue go-zpkg/zbk 282 --body-file report.md          → posted, first try
//
// Two refusals and a round trip to the user, for a tool that was already
// installed and purpose-built — it reads the body from a FILE and sends the
// token only in a header, which is why it is the shape that gets through.
//
// ⛔ A NOTE DID NOT STOP IT, AND THE NOTE WAS MINE. "Look for an existing tool
// first" was already written in this fleet's standing rules and in a memory
// whose whole subject is that the tools have homes now. It did not fire,
// because nothing ran it: at the moment of typing `gh issue comment` the
// thought was "post the comment", not "audit the toolbox". That is this fleet's
// own test for when a lesson has to become a refusal — before the spend,
// not after it.
//
// It also costs more than a retry. `gh` is a general-purpose write primitive,
// so each hand-written form is judged on its own and two equivalent spellings
// can behave differently: in that same session `gh api -X PATCH …/pulls/443`
// went through while `gh api …/issues/282/comments` did not. A fleet program
// has ONE name, which can be allowed once.
//
// # What it refuses, stated as the ACT
//
// Changing GitHub state with `gh` when a fleet program does that exact act.
//
// The table is deliberately short. Every entry names a program that exists and
// is installed, because a refusal that cannot say what to do instead is a
// refusal that gets worked around — and a long table would drift out of date
// and start refusing things with no answer.
//
// # What it does NOT refuse, and that half is the point
//
//	gh issue list / view               reads: the fleet has no quarrel with them
//	gh pr checks --watch               a read, and the documented way to wait
//	gh pr create --body-file b.md      how a pull request is opened here
//	gh api rate_limit                  a read, and free
//	gh api -X PATCH …/pulls/N          no fleet program edits a PR body yet
//	git commit -m "gh issue comment"   the words, not the act
//
// The last line is why this works on STATEMENTS and asks for `gh` in the
// command position: the phrase appears in commit messages and in prose about
// this very rule.
//
// # The escape
//
//	GITSAFE_ALLOW_RAW_GH=1
//
// in front of ONE command, which is an act rather than a setting. There are
// legitimate uses — a verb no fleet program covers yet, or reproducing a
// failure the program hides — and both are better for being deliberate.
//
// # What it deliberately does not try to know
//
// Whether the fleet program would SUCCEED. This refuses the hand-written form
// on the grounds that a purpose-built program exists, not on a prediction about
// the API. If the program then fails, that failure is a defect in a Go program
// with a test, in a repository, under semver — which is the whole point of
// having one.
package ghbyhand

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Escape is the ASSIGNMENT that allows one command through, written in front of
// it, and it is read from the command's own text rather than from this
// process's environment.
//
// ⛔⛔ AN ENVIRONMENT VARIABLE CANNOT WORK HERE, AND THE FIRST VERSION OF THIS
// RULE USED ONE. A PreToolUse hook runs BEFORE any shell, so
// `GITSAFE_ALLOW_RAW_GH=1 gh issue comment …` reaches the guard as a STRING:
// nothing has assigned anything yet, and os.Getenv is empty. `hookcheck` caught
// it on the installed binary — the escape row answered "refuse" where the
// corpus said "allow" — after a green local suite. [volatile], [stalebase],
// [discard] and [bypass] already read their escapes this way, and
// discard.Escape records why; this is the fifth site with that shape, and
// extracting it is a change of its own rather than a line in this one.
const Escape = "GITSAFE_ALLOW_RAW_GH"

// Advice is what to do instead, in general. A guard that says no without
// saying what to do gets worked around.
const Advice = "The fleet programs take the body from a FILE and the token from a " +
	"file, never from a command line: that is why they are the shape that is " +
	"allowed, and it is also the shape that cannot splice prose into an " +
	"argument. To find out whether a program already covers something, ask " +
	"rather than remember: `ls ~/.local/bin`, or `fleetverbs --match <word> " +
	"--cli ~/Documents/VCS/GIT/localhost/go-fleettools-fleettools` which reads " +
	"the source and answers \"does anything here already do X?\". `" + Escape +
	"=1` in front of one command is the escape."

// rule maps a hand-written act to the program that performs it.
type rule struct {
	verb    string // the `gh` sub-command, e.g. "issue comment"
	act     string // what it would do, in words
	instead string // the fleet program, with its real argument shape
}

// rules is the whole table. Each `instead` is a program installed in
// ~/.local/bin, with the argument shape its own --help prints: a refusal that
// names a command nobody can run is worse than no refusal.
var rules = []rule{
	{"issue comment", "comment on an issue",
		"ghissue <owner/repo> <number> --body-file <file>"},
	{"pr comment", "comment on a pull request",
		"ghissue <owner/repo> <number> --body-file <file> (it handles both)"},
	{"issue close", "close an issue",
		"ghissue <owner/repo> <number> --close --reason completed"},
	{"issue reopen", "reopen an issue",
		"ghissue <owner/repo> <number> --reopen"},
	{"pr merge", "merge a pull request",
		"ghmerge <number>, or ghrelease <number> <tag> to merge and tag it"},
	{"repo create", "create a repository",
		"ghnew -public|-private <name>"},
}

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Match is the statement that matched, shortened for a message.
	Match string
	// Act is what that statement would do, in words.
	Act string
	// Instead is the fleet program that does it.
	Instead string
}

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Instead != "" }

// Why names the act, for a caller that prints it.
func (f Finding) Why() string {
	return "it would " + f.Act + " by hand, and the fleet has a program for that act"
}

func (f Finding) String() string {
	if !f.Found() {
		return ""
	}
	return "it matched " + f.Match
}

// Check says whether cmd changes GitHub state by hand where a fleet program
// covers the act.
func Check(cmd string) Finding {
	for _, st := range shellcmd.Statements(shellcmd.StripQuotedHeredocs(cmd)) {
		for _, stage := range shellcmd.Stages(st) {
			if f := checkStatement(stage); f.Found() {
				return f
			}
		}
	}
	return Finding{}
}

// checkStatement asks about one simple command.
func checkStatement(st string) Finding {
	fields := strings.Fields(st)
	for i := 0; i < len(fields) && isAssignment(fields[i]); i++ {
		if name, value, _ := strings.Cut(fields[i], "="); name == Escape && value != "" && value != "0" {
			return Finding{}
		}
	}
	words := command(st)
	if len(words) < 2 || !isGH(words[0]) {
		return Finding{}
	}
	// ⛔ ASKING A COMMAND WHAT IT DOES IS NOT DOING IT, and this rule refused
	// `gh pr merge --help` until hookcheck said so: that row was already in the
	// corpus, for the sibling rules, and it is the kind of false refusal that
	// gets a guard deleted. Reading the documentation of the thing you are
	// being told not to run is exactly what someone does next.
	for _, w := range words[1:] {
		if w == "--help" || w == "-h" {
			return Finding{}
		}
	}
	// Two words, because every verb in the table is `gh <noun> <verb>`. A
	// one-word `gh` invocation (`gh auth status`) cannot match.
	if len(words) >= 3 {
		pair := words[1] + " " + words[2]
		for _, r := range rules {
			if r.verb == pair {
				return Finding{Match: short(st), Act: r.act, Instead: r.instead}
			}
		}
	}
	if words[1] == "api" {
		return checkAPI(st, words)
	}
	return Finding{}
}

// checkAPI covers the same acts reached through the raw endpoint. Only the
// comment endpoints are refused, and only with a WRITE method: `gh api` is the
// escape hatch for everything the fleet does not cover, and refusing it whole
// would leave reads — rate_limit, check-runs — with nothing to use.
//
// ⛔ A WRITE IS NOT ALWAYS SPELLED. POSTing to a collection needs no -X at all:
// `gh api <path> -F body=@file` is a POST because a field was given, which is
// documented gh behaviour and is exactly the form that was typed on 2026-11-08.
// So a field-bearing call to a comments collection counts as a write.
func checkAPI(st string, words []string) Finding {
	path := ""
	for _, w := range words[2:] {
		if !strings.HasPrefix(w, "-") && strings.Contains(w, "/") {
			path = w
			break
		}
	}
	if path == "" || !isCommentsPath(path) {
		return Finding{}
	}
	if !writesAPI(words) {
		return Finding{}
	}
	return Finding{
		Match:   short(st),
		Act:     "comment on an issue or pull request",
		Instead: "ghissue <owner/repo> <number> --body-file <file>",
	}
}

// isCommentsPath reports whether the path addresses an issue's or pull
// request's comment collection. GitHub serves a pull request's comments from
// the ISSUES path, which is the detail that makes one rule cover both.
func isCommentsPath(path string) bool {
	return strings.Contains(path, "/issues/") && strings.HasSuffix(strings.TrimRight(path, "/"), "/comments")
}

// writesAPI reports whether this `gh api` call would change state: an explicit
// write method, or any field, which makes gh send a POST.
func writesAPI(words []string) bool {
	for i, w := range words {
		switch {
		case w == "-X" || w == "--method":
			if i+1 < len(words) && isWriteMethod(words[i+1]) {
				return true
			}
		case strings.HasPrefix(w, "--method="):
			if isWriteMethod(strings.TrimPrefix(w, "--method=")) {
				return true
			}
		case w == "-F" || w == "-f" || w == "--field" || w == "--raw-field" ||
			w == "--input" || strings.HasPrefix(w, "--field=") ||
			strings.HasPrefix(w, "--raw-field=") || strings.HasPrefix(w, "--input="):
			return true
		}
	}
	return false
}

func isWriteMethod(m string) bool {
	switch strings.ToUpper(m) {
	case "POST", "PATCH", "PUT", "DELETE":
		return true
	}
	return false
}

// command returns the words of a simple command with leading VAR=value
// assignments dropped, so `GH_TOKEN=x gh issue comment` is still seen as gh.
func command(st string) []string {
	var out []string
	for _, w := range strings.Fields(st) {
		if len(out) == 0 && isAssignment(w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// isAssignment reports whether a word is a VAR=value prefix rather than the
// command. The `=` must not be first, so `=x` is a command name and not an
// assignment.
func isAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	return i > 0 && !strings.ContainsAny(w[:i], "/'\"")
}

// isGH reports whether a command word invokes the GitHub CLI, including by an
// absolute path.
func isGH(w string) bool {
	w = strings.Trim(w, `"'`)
	if i := strings.LastIndexByte(w, '/'); i >= 0 {
		w = w[i+1:]
	}
	return w == "gh"
}

// short is an excerpt of the statement, so a refusal can point at something
// without quoting a whole heredoc.
func short(st string) string {
	st = strings.Join(strings.Fields(st), " ")
	const max = 72
	if len(st) > max {
		return st[:max] + "…"
	}
	return st
}
