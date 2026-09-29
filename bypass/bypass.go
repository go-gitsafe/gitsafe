// Package bypass answers one question: does this run by hand a command that a
// CHECKING wrapper exists for?
//
// Three rules lived in a shell script on one machine, as greps over the whole
// command text. Measured against the Go guard that runs beside them on
// 2026-09-29, the shell was wrong on 6 of 10 cases — four disclosures let
// through and two correct commands refused. Both false refusals were a QUOTED
// heredoc: a person writing ABOUT the forbidden command, which a grep cannot
// tell from running it.
//
// That is not a flaw in those particular patterns. A guard that reads a
// command line has to know where a command ENDS and a quotation begins, and
// searching the text for a word never will. So this asks the question the way
// [discard] does, and for the same reason:
//
//   - the body of a heredoc whose tag is quoted is removed first, through
//     [shellcmd.StripQuotedHeredocs], shared with every other rule here;
//   - the line is split at the separators that start a new command, so one
//     buried in a chain is judged too;
//   - and within each segment only the COMMAND POSITION counts. That is what
//     keeps `git commit -m 'mention git push in the message'` allowed — its
//     subcommand is commit, and the words after it are an argument. The first
//     draft of this package searched the text and refused exactly that, which
//     is the failure it exists to avoid, caught by its own corpus.
//
// # The three
//
//	git push            gitpush names a credential helper rather than reading
//	                    the token, refuses a remote URL that carries one, and
//	                    redacts what it prints. A token reached an output
//	                    stream twice before that wrapper existed.
//	gh pr merge         ghmerge asks GitHub whether a check actually RAN, not
//	                    whether anything is failing. `gh pr checks` prints "no
//	                    checks reported" and exits 0, so a filter for lines
//	                    that are not "pass" finds nothing wrong and merges.
//	                    That happened twice in one hour.
//	-c user.name=       git already knows who it is. Overriding it on the
//	-c user.email=      command line put a personal name on fourteen local
//	                    commits and, the same day, on published ones. The
//	                    history had to be rewritten both times.
//
// Each refusal names the wrapper and its arguments, because a refusal that
// does not say what to do instead is an obstacle rather than a rule.
package bypass

import (
	"path/filepath"
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing was
// found.
type Finding struct {
	// Rule names what matched: "push", "merge" or "identity".
	Rule string
	// Match is the fragment that triggered it, so a refusal can point at
	// something rather than assert.
	Match string
	// Why is one sentence a person can act on.
	Why string
	// Advice is what to do instead, including the escape where there is one.
	Advice string
}

// Found reports whether anything was found.
func (f Finding) Found() bool { return f.Rule != "" }

// Escape is the assignment that says a hand merge is meant. Like [discard]'s,
// it is honoured as a PREFIX ON THE COMMAND rather than in the guard's own
// environment: the guard runs before the command does, and reads it as text,
// so the variable the command would run with never reaches this process.
const Escape = "GITSAFE_HAND_MERGE"

// Check reports whether cmd runs by hand something a checking wrapper exists
// for.
func Check(cmd string) Finding {
	for _, part := range segments(shellcmd.StripQuotedHeredocs(cmd)) {
		if f := checkOne(part); f.Found() {
			return f
		}
	}
	return Finding{}
}

// segments splits at the separators that start a new command, so one buried in
// a chain is judged too.
func segments(cmd string) []string {
	f := func(r rune) bool { return r == ';' || r == '&' || r == '|' || r == '\n' }
	return strings.FieldsFunc(cmd, f)
}

func checkOne(part string) Finding {
	fields := strings.Fields(part)
	i := 0
	escaped := false
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "-") {
		if name, value, _ := strings.Cut(fields[i], "="); name == Escape && value != "" && value != "0" {
			escaped = true
		}
		i++
	}
	if i >= len(fields) {
		return Finding{}
	}
	switch base(fields[i]) {
	case "git":
		return checkGit(fields[i+1:])
	case "gh":
		if escaped {
			return Finding{}
		}
		return checkGH(fields[i+1:])
	}
	return Finding{}
}

// checkGit walks git's OWN options to reach the subcommand. `-c user.email=…`
// is refused wherever it appears among them; `push` counts only as the
// subcommand, so `git stash push` and `git commit -m '… git push …'` are not
// pushes.
func checkGit(args []string) Finding {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			if a == "push" {
				return pushFinding()
			}
			return Finding{} // the subcommand, and it is not push
		}
		// `-c k=v` and `-C dir` take a value, either joined or separate.
		if a == "-c" || a == "-C" {
			if i+1 < len(args) {
				i++
				if isIdentity(args[i]) {
					return identityFinding(args[i])
				}
			}
			continue
		}
		if isIdentity(a) {
			return identityFinding(a)
		}
	}
	return Finding{}
}

func isIdentity(s string) bool {
	s = strings.TrimPrefix(s, "-c")
	return strings.HasPrefix(s, "user.name=") || strings.HasPrefix(s, "user.email=")
}

// checkGH wants `pr` then `merge`, skipping options.
//
// gh's only GLOBAL flags are --help and --version, checked against `gh --help`
// on 2026-09-29, so `gh --repo o/r pr merge` is not a thing anyone can type
// and this does not try to allow for it. --repo belongs to the subcommand and
// comes after.
//
// --help merges nothing. The shell rule this replaces refused `gh pr merge
// --help` — it was reading the words, not the command — and that is the sixth
// false refusal counted in one session.
func checkGH(args []string) Finding {
	want := []string{"pr", "merge"}
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return Finding{}
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if len(want) == 0 {
			continue // `pr merge` already seen; keep scanning for --help
		}
		if a != want[0] {
			return Finding{}
		}
		want = want[1:]
	}
	if len(want) == 0 {
		return mergeFinding()
	}
	return Finding{}
}

func base(s string) string { return filepath.Base(s) }

func identityFinding(match string) Finding {
	return Finding{
		Rule:  "identity",
		Match: match,
		Why: "this passes an identity to git on the command line, and git already knows who it is — " +
			"overriding it has put a personal name on commits twice, and both times the history had to be rewritten",
		Advice: "Use plain `git commit`. If you are unsure who git thinks you are, ask it:\n    git config user.email",
	}
}

func pushFinding() Finding {
	return Finding{
		Rule:  "push",
		Match: "git push",
		Why: "this pushes with plain git, which reads the token itself — " +
			"a token reached an output stream here twice before the wrapper existed",
		Advice: "Push with gitpush, which takes the same arguments. It names a credential helper\n" +
			"rather than reading the token, refuses a remote URL that carries a credential,\n" +
			"and redacts what it prints.\n\n    gitpush origin main",
	}
}

func mergeFinding() Finding {
	return Finding{
		Rule:  "merge",
		Match: "gh pr merge",
		Why: `this merges a pull request by hand, and "nothing is failing" is not "everything passed" — ` +
			"a pull request with no merge ref never runs a workflow, and reads as green to any filter",
		Advice: "Use ghmerge, which refuses unless a check actually RAN, every check that ran\n" +
			"passed, and GitHub says the pull request is mergeable.\n\n" +
			"    ghmerge 42                  # in the repository, origin remote\n" +
			"    ghmerge owner/repo 42\n\n" +
			"If a merge really has to be done by hand, say so on purpose:\n" +
			"    " + Escape + "=1 gh pr merge …",
	}
}
