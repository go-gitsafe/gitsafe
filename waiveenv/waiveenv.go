// Package waiveenv refuses a command that assigns a fleet gate's waiver or skip
// variable, in whatever position the assignment sits.
//
// ⛔ Measured 2026-11-22 (go-fft docs round): four commands of the form
// `cd X && <WAIVER>=… gitpush …` ran with is_error:false. The operator's
// permissions.deny wildcard on the assignment did not fire, because Claude Code
// strips leading assignments from each sub-command BEFORE matching a pattern, so
// a pattern written over the assignment never sees it. Only a parse of the
// command can: this package does it, and guard-bash runs it with NO escape,
// since an escape for "set a waiver" would be the waiver.
//
// A name is a waiver when it is upper-case and ends in _SKIP, _WAIVE or _WAIVER
// (see [IsWaiverName]): a gate invented tomorrow is covered the day it appears.
// Quoted text (a commit message that names one), comments, quoted heredocs, and
// words after the command word (`echo X_SKIP=1`) are not assignments and are not
// refused.
package waiveenv

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Advice is appended to every refusal.
const Advice = `A gate that refuses is telling you something is wrong: fix what it names.
If the gate itself is wrong, file it with ghissue and wait for the fix; do not
set a waiver or a skip to get past it, in any position (after cd, behind env, in
a subshell, in bash -c, exported). The refusal has no escape.`

// Finding is one assignment of a waiver variable.
type Finding struct {
	Var   string // the variable's name
	Match string // the word as typed
}

// Found reports whether a waiver assignment was found.
func (f Finding) Found() bool { return f.Var != "" }

// Why is the reason, in a sentence.
func (f Finding) Why() string {
	return "it sets " + f.Var + ", a variable that waives or skips a fleet gate"
}

// String points at the text.
func (f Finding) String() string { return fmt.Sprintf("It matched `%s`", f.Match) }

var (
	nameRe   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*_(SKIP|WAIVE|WAIVER)$`)
	assignRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=`)
)

// IsWaiverName reports whether name is the name of a waiver or skip variable.
func IsWaiverName(name string) bool { return nameRe.MatchString(name) }

type word struct {
	raw    string // as typed, quotes included
	text   string // quotes removed
	single bool   // holds a single-quoted part: no substitution happens in it
}

// wrappers run the words after them as a command; the assignment they take (env)
// or pass on (the rest) still reaches the program.
var wrappers = map[string]bool{
	"sudo": true, "timeout": true, "nice": true, "nohup": true, "env": true,
	"command": true, "exec": true, "time": true, "builtin": true, "stdbuf": true,
	"ionice": true, "xargs": true, "doas": true, "setsid": true,
	"if": true, "then": true, "else": true, "elif": true, "do": true,
	"while": true, "until": true, "!": true, "{": true, "}": true,
}

// valueFlags take the next word as their value.
var valueFlags = map[string]bool{
	"-u": true, "-g": true, "-h": true, "-p": true, "-C": true, "-D": true,
	"-R": true, "-T": true, "-U": true, "-s": true, "-k": true, "-n": true,
	"-S": true, "-I": true, "-P": true, "-d": true, "-E": true, "-L": true,
	"-a": true, "-c": true, "-i": true, "-o": true, "-e": true,
}

var declarers = map[string]bool{"export": true, "declare": true, "typeset": true, "readonly": true, "local": true}

var shells = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true}

// Check parses cmd and returns the first waiver assignment, or a zero Finding.
func Check(cmd string) Finding {
	return check(shellcmd.StripQuotedHeredocs(cmd))
}

func check(cmd string) Finding {
	for _, words := range commands(cmd) {
		if f := scan(words); f.Found() {
			return f
		}
		for _, w := range words {
			if w.single || !(strings.Contains(w.raw, "$(") || strings.Contains(w.raw, "`")) {
				continue
			}
			inner := strings.NewReplacer("$(", " ; ", "`", " ; ", ")", " ; ", `"`, " ").Replace(w.raw)
			if f := check(inner); f.Found() {
				return f
			}
		}
	}
	return Finding{}
}

func scan(ws []word) Finding {
	afterEnv, skipNext, duration := false, false, false
	for i := 0; i < len(ws); i++ {
		w := ws[i]
		if skipNext {
			skipNext = false
			continue
		}
		if m := assignRe.FindStringSubmatch(w.raw); m != nil {
			if IsWaiverName(m[1]) {
				return Finding{Var: m[1], Match: w.raw}
			}
			continue
		}
		base := w.text
		if j := strings.LastIndex(base, "/"); j >= 0 {
			base = base[j+1:]
		}
		switch {
		case declarers[base]:
			for _, r := range ws[i+1:] {
				if m := assignRe.FindStringSubmatch(r.text); m != nil && IsWaiverName(m[1]) {
					return Finding{Var: m[1], Match: r.raw}
				}
			}
			return Finding{}
		case base == "eval":
			rest := make([]string, 0, len(ws))
			for _, r := range ws[i+1:] {
				rest = append(rest, r.text)
			}
			return check(strings.Join(rest, " "))
		case shells[base]:
			for _, r := range ws[i+1:] {
				if strings.ContainsAny(r.text, " \t\n") {
					if f := check(r.text); f.Found() {
						return f
					}
				}
			}
			return Finding{}
		case wrappers[base]:
			if base == "env" {
				afterEnv = true
			}
			duration = base == "timeout"
			// Options and their values, then the next word is re-read.
			for i+1 < len(ws) && strings.HasPrefix(ws[i+1].raw, "-") && len(ws[i+1].raw) > 1 {
				i++
				if valueFlags[ws[i].raw] && i+1 < len(ws) {
					i++
				}
			}
			if duration && i+1 < len(ws) && !assignRe.MatchString(ws[i+1].raw) {
				i++
			}
			continue
		case afterEnv:
			if m := assignRe.FindStringSubmatch(w.text); m != nil {
				if IsWaiverName(m[1]) {
					return Finding{Var: m[1], Match: w.raw}
				}
				continue
			}
			return Finding{}
		default:
			return Finding{}
		}
	}
	return Finding{}
}

// commands splits cmd into simple commands at unquoted ; & | ( ) ` and newlines,
// dropping comments, and cuts each into words that remember their quoting.
func commands(cmd string) [][]word {
	var out [][]word
	var cur []word
	var raw, text strings.Builder
	single, inWord := false, false
	flushWord := func() {
		if inWord {
			cur = append(cur, word{raw: raw.String(), text: text.String(), single: single})
		}
		raw.Reset()
		text.Reset()
		single, inWord = false, false
	}
	endCommand := func() {
		flushWord()
		if len(cur) > 0 {
			out = append(out, cur)
		}
		cur = nil
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			inWord = true
			raw.WriteRune(c)
			raw.WriteRune(rs[i+1])
			text.WriteRune(rs[i+1])
			i++
		case c == '\'':
			inWord, single = true, true
			raw.WriteRune(c)
			i++
			for ; i < len(rs) && rs[i] != '\''; i++ {
				raw.WriteRune(rs[i])
				text.WriteRune(rs[i])
			}
			if i < len(rs) {
				raw.WriteRune('\'')
			}
		case c == '"':
			inWord = true
			raw.WriteRune(c)
			i++
			for ; i < len(rs) && rs[i] != '"'; i++ {
				if rs[i] == '\\' && i+1 < len(rs) {
					raw.WriteRune(rs[i])
					i++
				}
				raw.WriteRune(rs[i])
				text.WriteRune(rs[i])
			}
			if i < len(rs) {
				raw.WriteRune('"')
			}
		case c == ' ' || c == '\t':
			flushWord()
		case c == '#' && !inWord:
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
			i--
		case c == '\n' || c == ';' || c == '&' || c == '|' || c == '(' || c == ')' || c == '`':
			endCommand()
		default:
			inWord = true
			raw.WriteRune(c)
			text.WriteRune(c)
		}
	}
	endCommand()
	return out
}
