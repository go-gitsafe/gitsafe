// Copyright (c) the go-gitsafe authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package plain decides whether a backtick is evaluated ONLY inside a
// deliberately small grammar, and refuses every backtick-bearing command outside
// it (#156).
//
// S = words, blanks, ; & | && || newline, < > redirections (not <<, <(, >( ),
// '…', "…", \x, $name / $special, and $( … ) nested to any depth with the same
// grammar inside. Anything else in code or double-quote context — an unquoted
// #, <<, $', $", ${, $[, $((, ((, a bare ( or ) , the words case/esac, an
// unclosed quote or substitution — makes the command UNCERTAIN.
//
// S+ adds QUOTED heredocs ([CheckHeredoc]).
//
// # Which shell each grammar is sound for
//
// Commands here run under zsh 5.9, never bash (see [ProductionScript]). S+ was
// measured sound for that zsh: 73.4 million fuzz execs and 3000 random in-grammar
// programs, none ran a backtick S+ allowed. It is NOT sound for bash 3.2.57,
// whose scan for the ) that closes $( … ) reads a quoted heredoc body (23 of the
// historical oracle inputs). So zsh gets S+ and bash gets strict S, which has no
// heredocs and was sound under both; any other shell gets a refusal.
package plain

// Verdict of Check.
type Verdict int

const (
	NoTick    Verdict = iota // no backtick at all: never refused
	Allow                    // in S, every backtick literal
	Refuse                   // in S, a backtick the shell evaluates
	Uncertain                // a backtick, and the command is outside S: refuse
)

// Reason names the construct that put the command outside S (for reports).
type Result struct {
	V      Verdict
	Reason string
}

type ctx byte

const (
	code   ctx = iota // top level or inside $( )
	double            // inside "…"
)

func isWord(c byte) bool {
	return c == '_' || c == '-' || c == '.' || c == '/' || c == '=' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// Check classifies cmd in the strict grammar S.
func Check(cmd string) Result { return check(cmd, false) }

// CheckHeredoc classifies cmd in S+ = S plus QUOTED heredocs (<<'W', <<"W",
// <<\W, with or without -), anywhere in code context, whose opener line holds
// nothing that spans a newline, and whose body ends at a line equal to W.
func CheckHeredoc(cmd string) Result { return check(cmd, true) }

type hdoc struct {
	word  string
	strip bool
}

// heredocOpener parses a quoted delimiter at cmd[i:] (just after << or <<-).
// It returns the delimiter and the index of its last byte, or ok=false.
func heredocOpener(cmd string, i int) (word string, end int, ok bool) {
	for i < len(cmd) && (cmd[i] == ' ' || cmd[i] == '\t') {
		i++
	}
	if i >= len(cmd) {
		return "", 0, false
	}
	q := cmd[i]
	var j int
	switch q {
	case '\'', '"':
		j = i + 1
		for j < len(cmd) && cmd[j] != q {
			if !isDelim(cmd[j]) {
				return "", 0, false
			}
			j++
		}
		if j >= len(cmd) || j == i+1 {
			return "", 0, false
		}
		word = cmd[i+1 : j]
	case '\\':
		j = i + 1
		for j < len(cmd) && isDelim(cmd[j]) {
			j++
		}
		if j == i+1 {
			return "", 0, false
		}
		word = cmd[i+1 : j]
		j--
	default:
		return "", 0, false // unquoted delimiter: an expanding body
	}
	// the delimiter word must end there
	if j+1 < len(cmd) && !(cmd[j+1] == ' ' || cmd[j+1] == '\t' || cmd[j+1] == '\n' ||
		cmd[j+1] == ';' || cmd[j+1] == '|' || cmd[j+1] == '&' || cmd[j+1] == ')') {
		return "", 0, false
	}
	return word, j, true
}

func isDelim(c byte) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func check(cmd string, heredocs bool) Result {
	hasTick := false
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == '`' {
			hasTick = true
			break
		}
	}
	if !hasTick {
		return Result{V: NoTick}
	}
	unc := func(why string) Result { return Result{V: Uncertain, Reason: why} }
	// stack: each entry is a context; a code entry above depth 0 is a $( ).
	stack := []ctx{code}
	evaluated := false
	var pending []hdoc
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		top := stack[len(stack)-1]
		if top == double {
			switch c {
			case '\n':
				if len(pending) > 0 {
					return unc("newline in quotes with a heredoc pending")
				}
			case '"':
				stack = stack[:len(stack)-1]
			case '\\':
				if i+1 >= len(cmd) {
					return unc("trailing backslash")
				}
				i++
			case '`':
				evaluated = true
			case '$':
				if i+1 < len(cmd) {
					switch cmd[i+1] {
					case '(':
						if i+2 < len(cmd) && cmd[i+2] == '(' {
							return unc("$((")
						}
						stack = append(stack, code)
						i++
					case '{', '[', '\'', '"':
						return unc("$" + string(cmd[i+1]) + " in double quotes")
					}
				}
			}
			continue
		}
		// code context
		switch c {
		case '\'':
			j := i + 1
			for j < len(cmd) && cmd[j] != '\'' {
				j++
			}
			if j >= len(cmd) {
				return unc("unclosed '")
			}
			if len(pending) > 0 {
				for k := i; k < j; k++ {
					if cmd[k] == '\n' {
						return unc("newline in quotes with a heredoc pending")
					}
				}
			}
			i = j
		case '"':
			stack = append(stack, double)
		case '\\':
			if i+1 >= len(cmd) {
				return unc("trailing backslash")
			}
			i++
		case '`':
			evaluated = true
		case '$':
			if i+1 < len(cmd) {
				n := cmd[i+1]
				switch {
				case n == '(':
					if i+2 < len(cmd) && cmd[i+2] == '(' {
						return unc("$((")
					}
					stack = append(stack, code)
					i++
				case n == '{' || n == '[' || n == '\'' || n == '"':
					return unc("$" + string(n))
				case n == '#' || n == '?' || n == '$' || n == '!' || n == '*' || n == '@' || n == '-':
					i++
				}
			}
		case '#':
			return unc("unquoted #")
		case '\n':
			// a newline in code context: pending heredoc bodies start here
			if len(pending) == 0 {
				continue
			}
			pos := i + 1
			for _, h := range pending {
				found := false
				for pos <= len(cmd) {
					e := pos
					for e < len(cmd) && cmd[e] != '\n' {
						e++
					}
					line := cmd[pos:e]
					if h.strip {
						for len(line) > 0 && line[0] == '\t' {
							line = line[1:]
						}
					}
					pos = e + 1
					if line == h.word {
						found = true
						break
					}
					if e >= len(cmd) {
						break
					}
				}
				if !found {
					return unc("heredoc body never ends")
				}
			}
			pending = pending[:0]
			i = pos - 2 // the loop's i++ lands on the newline that ended the last terminator
			if i < 0 {
				i = 0
			}
		case '<':
			if heredocs && i+2 < len(cmd) && cmd[i+1] == '<' && cmd[i+2] == '<' {
				i += 2 // here-string: an ordinary word follows
				continue
			}
			if heredocs && i+1 < len(cmd) && cmd[i+1] == '<' {
				k, strip := i+2, false
				if k < len(cmd) && cmd[k] == '-' {
					k++
					strip = true
				}
				w, end, ok := heredocOpener(cmd, k)
				if !ok {
					return unc("<< unquoted or odd delimiter")
				}
				pending = append(pending, hdoc{w, strip})
				i = end
				continue
			}
			if i+1 < len(cmd) && (cmd[i+1] == '<' || cmd[i+1] == '(') {
				return unc("<< or <(")
			}
		case '>':
			if i+1 < len(cmd) && cmd[i+1] == '(' {
				return unc(">(")
			}
		case '(':
			return unc("bare (")
		case ')':
			if len(stack) == 1 {
				return unc("bare )")
			}
			if len(pending) > 0 {
				return unc(") with a heredoc pending")
			}
			stack = stack[:len(stack)-1]
		default:
			if isWord(c) && (i == 0 || !isWord(cmd[i-1])) {
				j := i
				for j < len(cmd) && isWord(cmd[j]) {
					j++
				}
				if w := cmd[i:j]; w == "case" || w == "esac" {
					return unc("case")
				}
			}
		}
	}
	if len(stack) != 1 {
		return unc("unclosed quote or substitution")
	}
	if len(pending) > 0 {
		return unc("heredoc with no body")
	}
	if evaluated {
		return Result{V: Refuse}
	}
	return Result{V: Allow}
}
