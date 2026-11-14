package shellcmd

import "strings"

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
		kind  byte // 'D' double quotes, 'P' $( … ), 'A' $(( … ))
		depth int
	}
	var stack []frame
	var pending []heredoc
	top := func() byte {
		if len(stack) == 0 {
			return 'C'
		}
		return stack[len(stack)-1].kind
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
			case c == '$' && next == '(':
				fill(i, i+2, ClassCode)
				if i+2 < len(s) && s[i+2] == '(' {
					cls[i+2] = ClassCode
					stack = append(stack, frame{'A', 2})
					i += 3
				} else {
					stack = append(stack, frame{'P', 1})
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
			stack = append(stack, frame{'D', 0})
			i++
		case c == '$' && next == '(':
			fill(i, i+2, ClassCode)
			if i+2 < len(s) && s[i+2] == '(' {
				cls[i+2] = ClassCode
				stack = append(stack, frame{'A', 2})
				i += 3
			} else {
				stack = append(stack, frame{'P', 1})
				i += 2
			}
		case c == '(' && (top() == 'P' || top() == 'A'):
			stack[len(stack)-1].depth++
			cls[i] = ClassCode
			i++
		case c == ')' && (top() == 'P' || top() == 'A'):
			stack[len(stack)-1].depth--
			if stack[len(stack)-1].depth == 0 {
				stack = stack[:len(stack)-1]
			}
			cls[i] = ClassCode
			i++
		case c == '#' && wordStart(s, i):
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
			if !ok || top() == 'A' {
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
				i = heredocBodies(s, cls, i, pending)
				pending = nil
			}
		default:
			cls[i] = ClassCode
			i++
		}
	}
	return cls
}

// wordStart reports whether a # at i begins a comment.
func wordStart(s string, i int) bool {
	return i == 0 || strings.IndexByte(" \t\n;&|()", s[i-1]) >= 0
}

// parseHeredoc reads the tag after `<<` at j. The tag is a shell WORD, so it may
// hold hyphens and dots (`<<'END-MSG'`); quoting any part of it makes the body
// literal. A tag that starts with a digit is a shift (`1<<2`), not a heredoc.
func parseHeredoc(s string, j int) (h heredoc, end int, ok bool) {
	if j < len(s) && s[j] == '-' {
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
		for pos < len(s) {
			end := strings.IndexByte(s[pos:], '\n')
			lineEnd := len(s)
			if end >= 0 {
				lineEnd = pos + end
			}
			term := strings.TrimSpace(s[pos:lineEnd]) == h.tag
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
				break
			}
		}
	}
	return pos
}
