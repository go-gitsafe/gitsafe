package chainedgate

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// # Required: `gh pr create --body-file F` only behind the checks on F
//
// [Check] refuses a gate and its write joined by something other than `&&`. It
// cannot refuse a write with NO gate, and that is the larger hole: five pull
// requests written by agents on 2026-11-12 (go-fleettools/fleettools#251 and
// #255, go-fft/fft#131, #138 and #140) were each refused by `ghmerge` at merge
// time for a bare issue reference, and the body was rewritten by hand after
// review and CI, at the last step. `prbody` was installed throughout; nothing
// ran it. The same gap exists for `paritycheck text` (fleettools v0.106.0 and
// later): `ghissue` and `ghpr` run it, `gh pr create` does not.
//
// So `gh pr create` — the one GitHub write that is still plain `gh`, because it
// is how a pull request is opened — is refused unless the same command line
// FIRST runs, joined by `&&` to it:
//
//	prbody F --repo o/r && paritycheck text F && gh pr create ... --body-file F
//
// ⛔ BOTH CHECKS, AND ON THE SAME FILE. One of them is the half-rule that let
// each of the five through; a check on another file decides nothing about this
// body. `&&` is what makes the shell enforce the verdicts, as in [Check].
//
// A `gh pr create` with no `--body-file` is not this rule's: bodies come from
// files, and the hand-written forms are refused by other rules.

// Missing is a `gh pr create` whose body file no check ran on first. The zero
// value means nothing found.
type Missing struct {
	// File is the body file the pull request is created from.
	File string
	// Repo is the repository the command names, or a placeholder.
	Repo string
	// Checks are the checks that were not chained before it.
	Checks []string
	// Write is the statement that creates the pull request.
	Write string
}

// Found reports whether this is a refusal.
func (m Missing) Found() bool { return m.File != "" }

// Why names the act.
func (m Missing) Why() string {
	return "it opens a pull request from `" + m.File + "` without first running " +
		strings.Join(m.Checks, " and ") + " on that file, joined by `&&` to the `gh pr create`"
}

func (m Missing) String() string {
	if !m.Found() {
		return ""
	}
	return "it matched `" + firstWords(m.Write) + "` … `--body-file " + m.File + "`"
}

// Instead is the exact line to type.
func (m Missing) Instead() string {
	return "prbody " + m.File + " --repo " + m.Repo + " && paritycheck text " + m.File + " && " + strings.TrimSpace(m.Write)
}

// RequiredAdvice explains the two checks.
const RequiredAdvice = "`prbody` reads the body for issue references that say nothing about what " +
	"happens to them (the question `ghmerge` asks at merge time, asked before the PR exists); " +
	"`paritycheck text` refuses a body that parks a performance change on bit identity " +
	"(fleet rule, go-fft/fft#121). Chained with `&&`, a refusal stops the `gh pr create`."

// Require says whether cmd opens a pull request from a body file without both
// checks chained before it.
func Require(cmd string) Missing {
	sts := statementsSep(shellcmd.StripQuotedHeredocs(cmd))
	for j, st := range sts {
		file, repo, ok := prCreateBody(st.text)
		if !ok {
			continue
		}
		// The chain is the run of statements joined to this one by `&&`.
		start := j
		for start > 0 && sts[start].sep == "&&" {
			start--
		}
		var hasBody, hasParity bool
		for _, c := range sts[start:j] {
			hasBody = hasBody || runs(c.text, "prbody", "", file)
			hasParity = hasParity || runs(c.text, "paritycheck", "text", file)
		}
		var missing []string
		if !hasBody {
			missing = append(missing, "`prbody "+file+" --repo "+repo+"`")
		}
		if !hasParity {
			missing = append(missing, "`paritycheck text "+file+"`")
		}
		if len(missing) > 0 {
			return Missing{File: file, Repo: repo, Checks: missing, Write: st.text}
		}
	}
	return Missing{}
}

// prCreateBody reports whether a statement is `gh pr create` with a body file,
// and returns the file and the repository it names ("<owner/repo>" when none).
func prCreateBody(st string) (file, repo string, ok bool) {
	for _, stage := range shellcmd.Stages(st) {
		ws := words(stage)
		if len(ws) < 3 || base(ws[0]) != "gh" || ws[1] != "pr" || ws[2] != "create" {
			continue
		}
		repo = "<owner/repo>"
		for i := 3; i < len(ws); i++ {
			w := ws[i]
			switch {
			case (w == "--body-file" || w == "-F") && i+1 < len(ws):
				file = unquote(ws[i+1])
			case strings.HasPrefix(w, "--body-file="):
				file = unquote(strings.TrimPrefix(w, "--body-file="))
			case (w == "--repo" || w == "-R") && i+1 < len(ws):
				repo = unquote(ws[i+1])
			case strings.HasPrefix(w, "--repo="):
				repo = unquote(strings.TrimPrefix(w, "--repo="))
			}
		}
		// Standard input is not a file anyone can check first; other rules refuse it.
		if file != "" && file != "-" {
			return file, repo, true
		}
	}
	return "", "", false
}

// runs reports whether a statement invokes tool (and, when sub is set, its
// first operand) on file.
func runs(st, tool, sub, file string) bool {
	for _, stage := range shellcmd.Stages(st) {
		ws := words(stage)
		if len(ws) < 2 || base(ws[0]) != tool {
			continue
		}
		if sub != "" && ws[1] != sub {
			continue
		}
		for _, w := range ws[1:] {
			if unquote(w) == file {
				return true
			}
		}
	}
	return false
}

// unquote drops one layer of matching quotes.
func unquote(w string) string {
	if len(w) >= 2 && (w[0] == '"' || w[0] == '\'') && w[len(w)-1] == w[0] {
		return w[1 : len(w)-1]
	}
	return w
}
