package shellcmd

import (
	"regexp"
	"strings"
)

// caseWord finds `case` as a word the shell may take for the keyword. It is
// deliberately broad: a false hit only makes the classifier unsure.
var caseWord = regexp.MustCompile(`(^|[\s;&|(])case(\s|$)`)

// The class of one byte of a command line, as [Classify] reports it.
const (
	// ClassCode is a byte the shell reads as syntax: a backtick here runs.
	ClassCode byte = 'c'
	// ClassDouble is text inside "…", where `$(…)` and a backtick still run.
	ClassDouble byte = 'd'
	// ClassSingle is text inside '…' or $'…': literal, nothing runs.
	ClassSingle byte = 's'
	// ClassComment is a # comment, from the # to the end of its line.
	ClassComment byte = 'k'
	// ClassHeredocQuoted is the body (and terminator) of a heredoc whose tag is
	// quoted: literal, nothing runs.
	ClassHeredocQuoted byte = 'h'
	// ClassHeredocExpanded is the body of an UNQUOTED heredoc: the shell expands
	// it, so a backtick runs, but a quote inside it is just a character.
	ClassHeredocExpanded byte = 'u'
	// ClassEscaped is a byte after a backslash, which is literal.
	ClassEscaped byte = 'e'
)

type heredoc struct {
	tag    string
	quoted bool
	dash   bool // <<-: leading TABS of the terminator line are ignored
}

// Classify says, for every byte of cmd, how the shell would read it. It is the
// one lexer behind [StripQuotedHeredocs] and the literal-backtick rule, so that
// "is this text quoted, commented, or a heredoc body?" has one answer.
//
// It knows '…', $'…' (where \' does not end the string), "…" with nested $(…),
// # comments, backslash escapes, and heredocs — an opener only counts OUTSIDE
// quotes, so `echo "<<'EOF'"` opens nothing. A quote that never closes is
// classed as code: a guard that is unsure must refuse, not allow.
func Classify(cmd string) []byte {
	s := cmd
	cls := make([]byte, len(s))
	type frame struct {
		kind  byte // 'D' double quotes, 'P' $( … ), 'A' $(( … )) or (( … )), 'B' ${ … }
		depth int
		dq    bool // 'B' opened inside double quotes
		start int  // where the frame opened
	}
	var stack []frame
	var pending []heredoc
	top := func() byte {
		if len(stack) == 0 {
			return 'C'
		}
		return stack[len(stack)-1].kind
	}
	// In "${x:-'…'}" the single quotes are NOT quoting, so a backtick between
	// them runs: the classifier leaves them as code, and the rule refuses.
	plainSingle := func() bool {
		// Arithmetic does not quote with ' either: $(('`x`')) runs the backtick.
		if len(stack) > 0 && stack[len(stack)-1].kind == 'A' {
			return true
		}
		return len(stack) > 0 && stack[len(stack)-1].kind == 'B' && stack[len(stack)-1].dq
	}
	fill := func(from, to int, c byte) {
		for k := from; k < to && k < len(s); k++ {
			cls[k] = c
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		var next byte
		if i+1 < len(s) {
			next = s[i+1]
		}
		if top() == 'D' {
			switch {
			case c == '\\' && next != 0:
				cls[i], cls[i+1] = ClassDouble, ClassEscaped
				i += 2
			case c == '"':
				cls[i] = ClassDouble
				stack = stack[:len(stack)-1]
				i++
			case c == '$' && next == '$':
				cls[i], cls[i+1] = ClassDouble, ClassDouble
				i += 2
			case c == '$' && next == '{':
				fill(i, i+2, ClassCode)
				stack = append(stack, frame{kind: 'B', dq: true})
				i += 2
			case c == '$' && next == '(':
				fill(i, i+2, ClassCode)
				if i+2 < len(s) && s[i+2] == '(' {
					cls[i+2] = ClassCode
					stack = append(stack, frame{kind: 'A', depth: 2})
					i += 3
				} else {
					stack = append(stack, frame{kind: 'P', depth: 1, start: i})
					i += 2
				}
			default:
				cls[i] = ClassDouble
				i++
			}
			continue
		}
		switch {
		case c == '\\' && next != 0:
			cls[i], cls[i+1] = ClassCode, ClassEscaped
			i += 2
		case c == '$' && next == '$':
			fill(i, i+2, ClassCode)
			i += 2
		case c == '$' && next == '{':
			fill(i, i+2, ClassCode)
			stack = append(stack, frame{kind: 'B', dq: plainSingle()})
			i += 2
		case c == '}' && top() == 'B':
			stack = stack[:len(stack)-1]
			cls[i] = ClassCode
			i++
		case plainSingle() && (c == '\'' || (c == '$' && next == '\'')):
			cls[i] = ClassCode
			i++
		case c == '$' && next == '\'':
			j := i + 2
			for j < len(s) && s[j] != '\'' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(s) {
				fill(i, j+1, ClassSingle)
				i = j + 1
			} else {
				cls[i] = ClassCode
				i++
			}
		case c == '\'':
			if j := strings.IndexByte(s[i+1:], '\''); j >= 0 {
				fill(i, i+j+2, ClassSingle)
				i += j + 2
			} else {
				cls[i] = ClassCode
				i++
			}
		case c == '"':
			cls[i] = ClassDouble
			stack = append(stack, frame{kind: 'D'})
			i++
		case c == '$' && next == '(':
			fill(i, i+2, ClassCode)
			if i+2 < len(s) && s[i+2] == '(' {
				cls[i+2] = ClassCode
				stack = append(stack, frame{kind: 'A', depth: 2})
				i += 3
			} else {
				stack = append(stack, frame{kind: 'P', depth: 1, start: i})
				i += 2
			}
		case c == '(' && (top() == 'P' || top() == 'A'):
			stack[len(stack)-1].depth++
			cls[i] = ClassCode
			i++
		case c == ')' && (top() == 'P' || top() == 'A'):
			f := &stack[len(stack)-1]
			f.depth--
			// ⛔ $(( … ) that is not closed by )) is a command substitution holding
			// a subshell, which is not arithmetic at all. Unsure refuses.
			if f.kind == 'A' && f.depth == 1 && next != ')' {
				for k := i; k < len(s); k++ {
					cls[k] = ClassCode
				}
				return cls
			}
			// ⛔ A `case` pattern ends in a ) that closes nothing, so which ) ends a
			// $( … ) holding one cannot be known. UNSURE ALWAYS RESOLVES TO
			// REFUSING: the rest of the command is code that no quote can change,
			// never a frame in which a ' still quotes.
			if f.depth == 0 && f.kind == 'P' && caseWord.MatchString(s[f.start:i]) {
				for k := i; k < len(s); k++ {
					cls[k] = ClassCode
				}
				return cls
			} else if f.depth == 0 {
				stack = stack[:len(stack)-1]
			}
			cls[i] = ClassCode
			i++
		case c == '(' && next == '(':
			fill(i, i+2, ClassCode)
			stack = append(stack, frame{kind: 'A', depth: 2})
			i += 2
		case c == '(' && next != ')' && !parenIsShell(s, cls, i):
			// ⛔ FOUND BY THE zsh ORACLE (#121): a ( that follows an ARGUMENT is a
			// syntax error in bash and a glob group or qualifier in zsh, which
			// substitutes the backticks before it complains of the pattern, and
			// reads what follows (a # after the ( or a |, a newline) by glob
			// rules: `: (E`+newline+`#`+T creates the file in zsh. No comment or
			// quote that follows can be trusted. Unsure refuses.
			for k := i; k < len(s); k++ {
				cls[k] = ClassCode
			}
			return cls
		case c == '#' && i > 0 && (s[i-1] == '(' || s[i-1] == '|') && cls[i-1] == ClassCode:
			// ⛔ FOUND BY THE zsh ORACLE (#121). After a ( or a | bash reads # as a
			// comment, but zsh reads `(#` as the opening of a glob flag and `a(`…`)`
			// or `(|#` as a glob pattern, and it substitutes the backticks BEFORE it
			// complains of the pattern: `echo a(#`+T+` and `: (|#`+T creates the file
			// in zsh and is a comment in bash. Which shell runs the line is not
			// knowable here, so the rest is code that no comment can hide. Unsure
			// refuses.
			for k := i; k < len(s); k++ {
				cls[k] = ClassCode
			}
			return cls
		case c == '#' && wordStart(s, cls, i) && top() != 'C':
			// ⛔ A comment inside $( … ) or ${ … }: bash finds the closing ) before
			// it reads the comment, so what a comment hides there is not what it
			// hides on its own line. Unsure refuses.
			for k := i; k < len(s); k++ {
				cls[k] = ClassCode
			}
			return cls
		case c == '#' && wordStart(s, cls, i):
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				end = len(s) - i
			}
			fill(i, i+end, ClassComment)
			i += end
		case c == '<' && next == '<':
			if i+2 < len(s) && s[i+2] == '<' { // <<< is a here-string
				fill(i, i+3, ClassCode)
				i += 3
				continue
			}
			h, end, ok := parseHeredoc(s, i+2)
			if !ok || top() == 'A' || top() == 'B' {
				fill(i, i+2, ClassCode)
				i += 2
				continue
			}
			fill(i, end, ClassCode)
			pending = append(pending, h)
			i = end
		case c == '\n':
			cls[i] = ClassCode
			i++
			if len(pending) > 0 {
				from := i
				expanded := false
				for _, h := range pending {
					expanded = expanded || !h.quoted
				}
				i = heredocBodies(s, cls, i, pending)
				pending = nil
				// ⛔ An UNQUOTED body expands, and a $( in it can run past the
				// terminator line in dash. Unsure refuses.
				if expanded && strings.Contains(s[from:i], "$(") {
					for k := from; k < len(s); k++ {
						cls[k] = ClassCode
					}
					return cls
				}
				// ⛔ Inside $( … ) bash looks for the closing ) before it reads a
				// heredoc, so a ) in the body can end the substitution early.
				// Unsure refuses.
				if len(stack) > 0 && strings.Contains(s[from:i], ")") {
					for k := from; k < len(s); k++ {
						cls[k] = ClassCode
					}
					return cls
				}
			}
		default:
			cls[i] = ClassCode
			i++
		}
	}
	// ⛔ A frame still open at the end is a command this lexer did not follow to
	// its close: UNSURE RESOLVES TO REFUSING, so nothing in it is quoted.
	if len(stack) > 0 {
		for k := range cls {
			cls[k] = ClassCode
		}
	}
	return cls
}

// wordBefore returns the shell word that ENDS at j and where it starts. A word
// is a run of unquoted, unescaped bytes with no blank or operator in it, and it
// must begin at a boundary; otherwise ("", j) says there is no word to judge.
func wordBefore(s string, cls []byte, j int) (string, int) {
	const boundary = " \t\n;&|()<>"
	k := j
	for k > 0 && cls[k-1] == ClassCode && strings.IndexByte(boundary, s[k-1]) < 0 {
		k--
	}
	if k > 0 && (cls[k-1] != ClassCode || strings.IndexByte(boundary, s[k-1]) < 0) {
		return "", j
	}
	return s[k:j], k
}

// commandPos reports whether a COMMAND may start right after s[:e] in every
// shell: at the start, after ; | ( or a newline, after && , after one of the
// keywords that is itself a standalone word in command position, or after the
// `in` of `case WORD in`. Anything else is an argument. Unsure is false.
func commandPos(s string, cls []byte, e int) bool {
	j := e
	for j > 0 && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	if j == 0 {
		return true
	}
	if cls[j-1] != ClassCode {
		return false
	}
	switch c := s[j-1]; {
	case c == ';' || c == '|' || c == '(' || c == '\n':
		return true
	case c == '&':
		return j < 2 || (s[j-2] != '>' && s[j-2] != '<')
	}
	w, k := wordBefore(s, cls, j)
	if j == e { // the word runs into what follows: `{(`, `if(`
		return false
	}
	switch w {
	case "then", "do", "else", "elif", "if", "while", "until", "time", "!", "{", "[[", "coproc":
		return commandPos(s, cls, k)
	case "in":
		// `in` is a keyword only after `case WORD`; `echo in (x)` is an argument.
		p := skipBlanks(s, k)
		subject, k2 := wordBefore(s, cls, p)
		p2 := skipBlanks(s, k2)
		kw, k3 := wordBefore(s, cls, p2)
		return subject != "" && p < k && p2 < k2 && kw == "case" && commandPos(s, cls, k3)
	}
	return false
}

// skipBlanks moves j back over spaces and tabs.
func skipBlanks(s string, j int) int {
	for j > 0 && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return j
}

// parenIsShell reports whether the ( at i is shell syntax in every shell: the
// start of a command (a subshell or a group), an array assignment `name=(` or
// `name+=(` in command position, or a process substitution `<(` / `>(`. A ( that
// follows an argument, a `)`, a `}`-less `{`, a keyword that is not a standalone
// word, or anything else opens a glob group in zsh. Unsure is false.
func parenIsShell(s string, cls []byte, i int) bool {
	if i > 0 && cls[i-1] == ClassCode {
		switch s[i-1] {
		case '<', '>':
			return true
		case '=':
			w, k := wordBefore(s, cls, i)
			name := strings.TrimSuffix(strings.TrimSuffix(w, "="), "+")
			return name != "" && assignName.MatchString(name) && commandPos(s, cls, k)
		}
	}
	return commandPos(s, cls, i)
}

var assignName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// wordStart reports whether a # at i begins a comment.
//
// The byte before must be UNESCAPED code and a separator: `a\ #x` and `$(a)#x`
// are mid-word, and `)` is not a boundary.
func wordStart(s string, cls []byte, i int) bool {
	return i == 0 || (cls[i-1] == ClassCode && strings.IndexByte(" \t\n;&|(", s[i-1]) >= 0)
}

// parseHeredoc reads the tag after `<<` at j. The tag is a shell WORD, so it may
// hold hyphens and dots (`<<'END-MSG'`); quoting any part of it makes the body
// literal. A tag that starts with a digit is a shift (`1<<2`), not a heredoc.
func parseHeredoc(s string, j int) (h heredoc, end int, ok bool) {
	if j < len(s) && s[j] == '-' {
		h.dash = true
		j++
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	var tag strings.Builder
	first := j
loop:
	for j < len(s) {
		switch c := s[j]; {
		case c == '\'' || c == '"':
			k := strings.IndexByte(s[j+1:], c)
			if k < 0 {
				return h, 0, false
			}
			tag.WriteString(s[j+1 : j+1+k])
			h.quoted = true
			j += k + 2
		case c == '\\' && j+1 < len(s):
			tag.WriteByte(s[j+1])
			h.quoted = true
			j += 2
		case strings.IndexByte(" \t\n;&|<>()", c) >= 0:
			break loop
		default:
			tag.WriteByte(c)
			j++
		}
	}
	if tag.Len() == 0 || (!h.quoted && s[first] >= '0' && s[first] <= '9') {
		return h, 0, false
	}
	h.tag = tag.String()
	return h, j, true
}

// heredocBodies classes the bodies that start at pos, one per pending opener, up
// to and including each terminator line, and returns where the code resumes.
func heredocBodies(s string, cls []byte, pos int, pending []heredoc) int {
	for _, h := range pending {
		start, closed := pos, false
		for pos < len(s) {
			end := strings.IndexByte(s[pos:], '\n')
			lineEnd := len(s)
			if end >= 0 {
				lineEnd = pos + end
			}
			text := s[pos:lineEnd]
			if h.dash {
				text = strings.TrimLeft(text, "\t")
			}
			term := text == h.tag
			stop := lineEnd + 1
			if stop > len(s) {
				stop = len(s)
			}
			switch {
			case h.quoted:
				for k := pos; k < stop; k++ {
					cls[k] = ClassHeredocQuoted
				}
			case term:
				for k := pos; k < stop; k++ {
					cls[k] = ClassCode
				}
			default:
				for k := pos; k < stop; k++ {
					cls[k] = ClassHeredocExpanded
					if s[k] == '\\' && k+1 < stop {
						k++
						cls[k] = ClassEscaped
					}
				}
			}
			pos = stop
			if term {
				closed = true
				break
			}
		}
		// ⛔ A body that never ends is a construct the shell reads in ways this
		// does not follow (inside $( … ) bash runs what the body holds): UNSURE
		// RESOLVES TO REFUSING, so it is code.
		if !closed {
			for k := start; k < len(s); k++ {
				cls[k] = ClassCode
			}
		}
	}
	return pos
}
