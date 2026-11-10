// Package chainedgate answers one question: does this command line run a CHECK
// and then, in the same breath, the write that check was supposed to decide?
//
// # Why it exists
//
// `prbody` reads a pull-request body and says whether every issue it mentions
// carries a disposition — the same question `ghmerge` asks at merge time. It is
// a gate: its exit status is the decision. Written like this, it is not:
//
//	prbody body.md --repo o/r > out.txt 2>&1; gh pr create --body-file body.md
//
// The `;` runs the second command whatever the first said. The check ran, it
// printed its refusal, and the pull request was created anyway — three times on
// 2026-11-09 and 2026-11-10, by the same hand, each time followed by an edit to
// repair the body afterwards. Twice the refusal was even READ and acted on, one
// command too late.
//
// ⛔ THE FAILURE IS NOT THAT THE CHECK WAS SKIPPED. It is that the check and the
// act were put in one Bash call, so there was no moment between them in which a
// verdict could change anything. A rule that said "remember to look at prbody's
// output" would have been obeyed and would have changed nothing: the output WAS
// looked at, afterwards.
//
// # What it refuses, stated as the ACT
//
// A gate and the write it gates, in one command line, not joined by `&&`.
//
//	prbody b.md --repo o/r; gh pr create --body-file b.md
//	prbody b.md > o.txt 2>&1; echo $?; ghissue o/r --new --body-file b.md
//
// # What it does NOT refuse, and that half is the point
//
//	prbody b.md --repo o/r && gh pr create --body-file b.md
//	        the verdict DOES decide: `&&` runs the second only on success
//	prbody b.md --repo o/r
//	        the check alone, which is how it is normally run
//	gh pr create --body-file b.md
//	        the write alone, in its own call, after the check in another
//
// ⛔ `&&` IS ALLOWED ON PURPOSE AND IS NOT A LOOPHOLE. It is the one spelling
// in which the shell itself enforces what the rule is about — the write cannot
// run unless the gate exited 0. Refusing it too would leave no correct way to
// express the pair, and a rule with no correct form is a rule people route
// around rather than follow.
package chainedgate

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing found.
type Finding struct {
	// Gate is the check whose verdict was not waited for.
	Gate string
	// Write is the act that ran regardless.
	Write string
	// Instead is the correct spelling for this pair.
	Instead string
}

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Gate != "" }

// Why names the act.
func (f Finding) Why() string {
	return "it runs a check and the write that check decides, in ONE command line: " +
		"`" + f.Gate + "` cannot stop `" + f.Write + "` from the same breath"
}

func (f Finding) String() string {
	if !f.Found() {
		return ""
	}
	return "it matched `" + f.Gate + "` … `" + f.Write + "`"
}

// Advice is the general remedy.
const Advice = "Join them with `&&`, so the shell enforces the verdict — " +
	"`prbody b.md --repo o/r && gh pr create --body-file b.md` — or run the " +
	"check in its own call, READ it, and write afterwards. A `;` between them " +
	"means the check's answer reaches nobody in time."

// gate is one check and the writes whose verdict it decides.
type gate struct {
	// check is the command name of the gate.
	check string
	// writes are prefixes of the commands it decides, matched on the first
	// words of a statement so `gh pr create` is distinguished from `gh pr view`.
	writes [][]string
	// instead is the correct spelling.
	instead string
}

// gates is the table. One entry per measured occurrence, never per guess.
//
// ⛔ `gh pr view`, `gh pr list` and `ghissue … --edit` are deliberately ABSENT.
// A gate is about a write that cannot be undone cheaply; reading is not one,
// and a table that grew by analogy would refuse the ordinary way of looking
// something up.
var gates = []gate{{
	check: "prbody",
	writes: [][]string{
		{"gh", "pr", "create"},
		{"ghissue"},
		{"ghpr"},
	},
	instead: "prbody <body> --repo <owner/repo> && gh pr create --body-file <body>",
}}

// Check says whether cmd chains a gate with the write it gates.
func Check(cmd string) Finding {
	sts := statementsSep(shellcmd.StripQuotedHeredocs(cmd))
	for i, st := range sts {
		g, ok := gateIn(st.text)
		if !ok {
			continue
		}
		for j := i + 1; j < len(sts); j++ {
			if !writeIn(g, sts[j].text) {
				continue
			}
			// ⛔ EVERY separator between the two must be `&&`. One `;`
			// anywhere in the chain is enough for the write to run on a
			// refusal: `gate; echo $?; write` is exactly as broken as
			// `gate; write`, and reads as more careful.
			if allAnd(sts[i+1 : j+1]) {
				continue
			}
			return Finding{Gate: g.check, Write: firstWords(sts[j].text), Instead: g.instead}
		}
	}
	return Finding{}
}

// statement is one simple command and the separator that introduced it.
type statement struct {
	text string
	// sep is the separator BEFORE this statement: "&&", ";" or "" for the first.
	sep string
}

// statementsSep splits like [shellcmd.Statements] but KEEPS the separator,
// because here the separator is the whole question.
//
// ⛔ `||` ends a statement and is NOT `&&`: `gate || true; write` runs the
// write on a refusal just as `;` does, so it is recorded as a non-`&&` link.
func statementsSep(cmd string) []statement {
	var out []statement
	cur := &strings.Builder{}
	sep := ""
	q := byte(0)
	flush := func(next string) {
		out = append(out, statement{text: cur.String(), sep: sep})
		cur.Reset()
		sep = next
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if q != 0 {
			cur.WriteByte(c)
			if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			q = c
			cur.WriteByte(c)
		case ';', '\n':
			flush(";")
		case '&':
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				flush("&&")
				i++
				continue
			}
			cur.WriteByte(c)
		case '|':
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				flush("||")
				i++
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, statement{text: cur.String(), sep: sep})
	return out
}

// allAnd reports whether every statement in the span was introduced by `&&`.
func allAnd(span []statement) bool {
	for _, st := range span {
		if st.sep != "&&" {
			return false
		}
	}
	return true
}

// words returns a statement's words with any leading VAR=value assignments
// dropped, the way a shell reads them.
func words(st string) []string {
	ws := strings.Fields(st)
	for len(ws) > 0 && isAssignment(ws[0]) {
		ws = ws[1:]
	}
	return ws
}

func isAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for _, r := range w[:i] {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// gateIn reports which gate, if any, a statement invokes.
func gateIn(st string) (gate, bool) {
	// A pipeline stage can be the gate: `prbody b.md | tee log`.
	for _, stage := range shellcmd.Stages(st) {
		ws := words(stage)
		if len(ws) == 0 {
			continue
		}
		for _, g := range gates {
			if base(ws[0]) == g.check {
				return g, true
			}
		}
	}
	return gate{}, false
}

// writeIn reports whether a statement invokes one of this gate's writes.
func writeIn(g gate, st string) bool {
	for _, stage := range shellcmd.Stages(st) {
		ws := words(stage)
		for _, w := range g.writes {
			if len(ws) < len(w) {
				continue
			}
			ok := base(ws[0]) == w[0]
			for k := 1; ok && k < len(w); k++ {
				ok = ws[k] == w[k]
			}
			if ok {
				return true
			}
		}
	}
	return false
}

// firstWords names the offending command in a refusal, so it points rather
// than asserts.
func firstWords(st string) string {
	ws := words(st)
	if len(ws) > 3 {
		ws = ws[:3]
	}
	return strings.Join(ws, " ")
}

// base is the command name without its directory.
func base(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}
