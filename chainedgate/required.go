package chainedgate

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// # Required: `gh pr create` only behind the checks on its body file
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
// FIRST runs, joined by `&&` and IMMEDIATELY before it:
//
//	prbody F --repo o/r && paritycheck text F && gh pr create ... --body-file F
//
// ⛔ BOTH CHECKS, ON THE SAME FILE. One of them is the half-rule that let each
// of the five through; a check on another file decides nothing about this body.
//
// ⛔ ADJACENT. Nothing may stand between the checks and the create, because
// `prbody F && paritycheck text F && cp evil.md F && gh pr create --body-file F`
// checks one body and publishes another. The two checks are a block directly in
// front of the create, in either order, each a plain command (no pipeline) that
// does not redirect into F; anything before the block is none of this rule's
// business (`cd` and the like).
//
// ⛔ EVERY `gh pr create` NEEDS A BODY FILE. `--body "…"`, `--fill` and no body
// flag at all are refused here: they skip the checks by having nothing to run
// them on. (`--help` is a read.)
//
// ⛔ THE COMMAND IS READ AFTER THE SHELL WOULD READ IT. `g"h" pr create`,
// `env gh …`, `command gh …`, `exec gh …`, `bash -c "gh pr create …"` and
// `gh -R o/r pr create` are all `gh pr create`; a rule that matched the
// spelling would be passed by a different spelling. Words are unquoted,
// assignments and the env/command/exec/nohup/time prefixes are skipped, a
// shell's -c script is read as a command line of its own (to a depth of 4), and
// global flags before the subcommand are skipped.

// Missing is a `gh pr create` whose body file no check ran on first. The zero
// value means nothing found.
type Missing struct {
	// File is the body file the pull request is created from ("" when it has
	// none: see NoBody).
	File string
	// NoBody is a `gh pr create` with no --body-file at all.
	NoBody bool
	// Repo is the repository the command names, or a placeholder.
	Repo string
	// Checks are what was not chained immediately before it.
	Checks []string
	// Write is the statement that creates the pull request.
	Write string
}

// Found reports whether this is a refusal.
func (m Missing) Found() bool { return m.File != "" || m.NoBody }

// Why names the act.
func (m Missing) Why() string {
	if m.NoBody {
		return "it opens a pull request with no `--body-file`: `--body`, `--fill` and a missing body flag give the checks nothing to run on"
	}
	return "it opens a pull request from `" + m.File + "` without " +
		strings.Join(m.Checks, " and ") + " immediately before it, joined by `&&`"
}

func (m Missing) String() string {
	switch {
	case m.NoBody:
		return "it matched `" + firstWords(m.Write) + "` with no `--body-file`"
	case m.Found():
		return "it matched `" + firstWords(m.Write) + "` … `--body-file " + m.File + "`"
	}
	return ""
}

// Instead is the exact line to type.
func (m Missing) Instead() string {
	file := m.File
	write := strings.TrimSpace(m.Write)
	if m.NoBody {
		file = "<body-file>"
		write += " --body-file " + file
	}
	return "prbody " + file + " --repo " + m.Repo + " && paritycheck text " + file + " && " + write
}

// RequiredAdvice explains the two checks.
const RequiredAdvice = "`prbody` reads the body for issue references that say nothing about what " +
	"happens to them (the question `ghmerge` asks at merge time, asked before the PR exists); " +
	"`paritycheck text` refuses a body that parks a performance change on bit identity " +
	"(fleet rule, go-fft/fft#121). Chained with `&&` and nothing between, a refusal stops the `gh pr create`."

// maxDepth bounds how many `sh -c` layers are read.
const maxDepth = 4

// Require says whether cmd opens a pull request without both checks chained
// immediately before it.
func Require(cmd string) Missing { return require(cmd, 0) }

func require(cmd string, depth int) Missing {
	sts := statementsSep(shellcmd.StripQuotedHeredocs(cmd))
	for j, st := range sts {
		for _, stage := range shellcmd.Stages(st.text) {
			argv := normalise(stage)
			if script, ok := shellScript(argv); ok {
				if depth < maxDepth {
					if m := require(script, depth+1); m.Found() {
						return m
					}
				}
				continue
			}
			file, repo, isCreate := prCreate(argv)
			if !isCreate {
				continue
			}
			if file == "" {
				return Missing{NoBody: true, Repo: repo, Write: st.text}
			}
			if miss := unchecked(sts, j, file, repo); len(miss) > 0 {
				return Missing{File: file, Repo: repo, Checks: miss, Write: st.text}
			}
		}
	}
	return Missing{}
}

// unchecked returns the checks not standing as a block directly before
// statement j, each joined by `&&`.
func unchecked(sts []statement, j int, file, repo string) []string {
	// Walk back over the block: statements joined to the next by `&&`, each one
	// of the two checks on file, run plainly.
	var hasBody, hasParity bool
	for k := j - 1; k >= 0 && sts[k+1].sep == "&&"; k-- {
		switch {
		case plainCheck(sts[k].text, "prbody", "", file):
			hasBody = true
		case plainCheck(sts[k].text, "paritycheck", "text", file):
			hasParity = true
		default:
			k = -1 // anything else ends the block
		}
	}
	var missing []string
	if !hasBody {
		missing = append(missing, "`prbody "+file+" --repo "+repo+"`")
	}
	if !hasParity {
		missing = append(missing, "`paritycheck text "+file+"`")
	}
	return missing
}

// plainCheck reports whether a statement is exactly one command — tool [sub] with
// file among its operands — with no pipeline, and no redirection into file.
func plainCheck(st, tool, sub, file string) bool {
	stages := shellcmd.Stages(st)
	if len(stages) != 1 {
		return false
	}
	argv := normalise(stages[0])
	if len(argv) < 2 || base(argv[0]) != tool || (sub != "" && argv[1] != sub) {
		return false
	}
	named := false
	for _, w := range argv[1:] {
		if w == file {
			named = true
		}
	}
	return named && !redirectsInto(argv, file)
}

// redirectsInto reports whether any word redirects output into file: `> F`,
// `>F`, `>>F`, `2>F`, `&>F`.
func redirectsInto(argv []string, file string) bool {
	for i, w := range argv {
		t := strings.TrimLeft(w, "0123456789&")
		if !strings.HasPrefix(t, ">") {
			continue
		}
		target := strings.TrimLeft(t, ">|")
		if target == "" && i+1 < len(argv) {
			target = argv[i+1]
		}
		if target == file {
			return true
		}
	}
	return false
}

// prefixes are commands that run their operand: `env gh …` is gh.
var prefixes = map[string]bool{"env": true, "command": true, "exec": true, "nohup": true, "time": true, "builtin": true}

var shells = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true}

// normalise reads a simple command the way the shell would: unquoted words,
// leading VAR=value assignments and run-the-operand prefixes dropped.
func normalise(stage string) []string {
	ws := splitWords(stage)
	for len(ws) > 0 {
		switch {
		case isAssignment(ws[0]):
			ws = ws[1:]
		case prefixes[base(ws[0])]:
			ws = ws[1:]
			// env's own flags and assignments: `env -i X=1 -u Y gh …`.
			for len(ws) > 0 && (strings.HasPrefix(ws[0], "-") || isAssignment(ws[0])) {
				if ws[0] == "-u" || ws[0] == "-C" || ws[0] == "-S" {
					ws = ws[1:]
				}
				if len(ws) > 0 {
					ws = ws[1:]
				}
			}
		default:
			return ws
		}
	}
	return ws
}

// shellScript returns the script of `bash -c "…"` (and -lc, -ec, sh, zsh).
func shellScript(argv []string) (string, bool) {
	if len(argv) < 3 || !shells[base(argv[0])] {
		return "", false
	}
	for i := 1; i+1 < len(argv); i++ {
		w := argv[i]
		if strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.Contains(w, "c") {
			return argv[i+1], true
		}
	}
	return "", false
}

// prCreate reports whether argv is `gh pr create` (global flags before the
// subcommand skipped) and returns its body file and repository. `--help` is a
// read and is not a creation.
func prCreate(argv []string) (file, repo string, ok bool) {
	if len(argv) < 3 || base(argv[0]) != "gh" {
		return "", "", false
	}
	i := 1
	repo = "<owner/repo>"
	for i < len(argv) && strings.HasPrefix(argv[i], "-") {
		switch w := argv[i]; {
		case (w == "-R" || w == "--repo") && i+1 < len(argv):
			repo = argv[i+1]
			i += 2
		case w == "--hostname" && i+1 < len(argv):
			i += 2
		case strings.HasPrefix(w, "--repo="):
			repo = strings.TrimPrefix(w, "--repo=")
			i++
		default:
			i++
		}
	}
	if i+1 >= len(argv) || argv[i] != "pr" || argv[i+1] != "create" {
		return "", "", false
	}
	for k := i + 2; k < len(argv); k++ {
		w := argv[k]
		switch {
		case w == "--help" || w == "-h":
			return "", "", false
		case (w == "--body-file" || w == "-F") && k+1 < len(argv):
			if argv[k+1] != "-" { // standard input is not a file anyone can check first
				file = argv[k+1]
			}
		case strings.HasPrefix(w, "--body-file="):
			file = strings.TrimPrefix(w, "--body-file=")
		case (w == "--repo" || w == "-R") && k+1 < len(argv):
			repo = argv[k+1]
		case strings.HasPrefix(w, "--repo="):
			repo = strings.TrimPrefix(w, "--repo=")
		}
	}
	return file, repo, true
}

// splitWords splits a command into words the way a shell does, minus every
// expansion: quotes are removed and adjacent pieces joined, so `g"h"` is `gh`.
func splitWords(s string) []string {
	var out []string
	var cur strings.Builder
	has := false
	var q byte
	flush := func() {
		if cur.Len() > 0 || has {
			out = append(out, cur.String())
			cur.Reset()
			has = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q == '\'':
			if c == '\'' {
				q = 0
			} else {
				cur.WriteByte(c)
			}
		case q == '"':
			switch {
			case c == '"':
				q = 0
			case c == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0:
				i++
				cur.WriteByte(s[i])
			default:
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			q, has = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			has = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}
