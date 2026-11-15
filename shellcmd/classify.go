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
	// open reports whether a frame of kind k is open anywhere on the stack.
	open := func(k byte) bool {
		for _, f := range stack {
			if f.kind == k {
				return true
			}
		}
		return false
	}
	// In "${x:-'…'}" the single quotes are NOT quoting, so a backtick between
	// them runs: the classifier leaves them as code, and the rule refuses.
	plainSingle := func() bool {
		// Arithmetic does not quote with ' either: $(('`x`')) runs the backtick.
		if len(stack) > 0 && (stack[len(stack)-1].kind == 'A' || stack[len(stack)-1].kind == 'K') {
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
			case c == '$' && next == '[':
				fill(i, i+2, ClassCode)
				stack = append(stack, frame{kind: 'K', depth: 1})
				i += 2
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
		case c == ')' && top() == 'B' && open('P'):
			// ⛔ FOUND BY A FUZZ RUN (#124 family, bash 3.2.57): its scan for the )
			// that closes $( … ) does not track ${ … }, so in
			// `"$(${x:-case x in x) '`+T+`'<<'E'`+newline+`"` that ) ENDS the
			// substitution, and what follows is a double-quoted string in which the
			// backtick runs. Where the substitution ends is not knowable here.
			// Unsure refuses.
			for k := i; k < len(s); k++ {
				cls[k] = ClassCode
			}
			return cls
		case c == '}' && top() == 'B':
			stack = stack[:len(stack)-1]
			cls[i] = ClassCode
			i++
		case (top() == 'A' || top() == 'K') && (c == '\'' || c == '"' || (c == '$' && next == '\'')):
			// (The legacy `$[ … ]` is the same arithmetic frame, kind 'K', #131:
			// bash and zsh evaluate it and a ' is no quote there; dash has no $[.)
			// ⛔ ASKED OF THE SHELLS (#124), per variant (TestArithmeticQuotes):
			// where the )) that closes $(( … )) is when a quote stands inside
			// depends on the shell. bash 3.2.57 SKIPS a '…' or "…" span while it
			// looks for the )), so `$(('))'`+T+`))` is closed by the LAST )) and
			// the backtick after it runs; dash and zsh do not skip, they fail at
			// the quote, so the same text reads differently there. No reading is
			// right for all three, and `$(('`+T+`'))` must stay refused as well:
			// ' is no quote in arithmetic. Unsure refuses the rest of the line.
			for k := i; k < len(s); k++ {
				cls[k] = ClassCode
			}
			return cls
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
		case c == '$' && (next == '(' || next == '[') && (top() == 'A' || top() == 'K'):
			// ⛔ FOUND BY THE zsh ORACLE IN CI (#121): inside $(( … )) zsh scans
			// for the closing )) and takes ' as an ordinary byte, so where a $(
			// or a nested $(( ends is not what bash reads:
			// `$(($(('$('))`+T+`'))` runs the backtick in zsh only. Unsure
			// refuses.
			for k := i; k < len(s); k++ {
				cls[k] = ClassCode
			}
			return cls
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
		case c == '$' && next == '[':
			fill(i, i+2, ClassCode)
			stack = append(stack, frame{kind: 'K', depth: 1})
			i += 2
		case c == '[' && top() == 'K':
			stack[len(stack)-1].depth++
			cls[i] = ClassCode
			i++
		case c == ']' && top() == 'K':
			cls[i] = ClassCode
			i++
			if stack[len(stack)-1].depth--; stack[len(stack)-1].depth == 0 {
				stack = stack[:len(stack)-1]
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
		case c == '#' && i > 0 && (s[i-1] == '(' || s[i-1] == '|' || s[i-1] == ')') && cls[i-1] == ClassCode:
			// ⛔ A # right after a ) too (fuzz, #124 family): after `(( ))` or a
			// `( … )` subshell the ) is an OPERATOR and bash starts a comment, as
			// in `(( ))#'E`+newline+T+`E`+newline+`'`; after `$( … )` it is
			// mid-word. This classifier cannot tell the two apart. Unsure refuses.
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
			if top() == 'B' && ok && strings.IndexByte(s[end:], '\n') >= 0 {
				// ⛔ FOUND BY A LONGER FUZZ RUN (#121, bash, not zsh): in
				// `$(${x:-case x in x) <<E`+newline+`'`+T+`'}…` bash reads the <<E as a
				// real heredoc whose UNQUOTED body runs the backtick, where this
				// classifier saw a ' that quotes it. Whether a ${ … } holds a
				// heredoc is not knowable here. Unsure refuses.
				for k := i; k < len(s); k++ {
					cls[k] = ClassCode
				}
				return cls
			}
			if !ok || top() == 'A' || top() == 'B' || top() == 'K' {
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
				//
				// ⛔ FOUND BY THE zsh ORACLE (#121): a backslash-newline in an
				// UNQUOTED body continues the line in zsh, which then does not see
				// the terminator and reads the body to the end of the command:
				// `<<-E`+newline+`\`+newline+tab+`E`+newline+`#`+T runs the backtick.
				// Where the body ends is not knowable here. Unsure refuses.
				if expanded && (strings.Contains(s[from:i], "$(") || strings.Contains(s[from:i], "\\\n")) {
					for k := from; k < len(s); k++ {
						cls[k] = ClassCode
					}
					return cls
				}
				// ⛔ Inside $( … ) bash looks for the closing ) before it reads the
				// heredoc, so a ) in the body can end the substitution early.
				// Unsure refuses. It is REAL: the fuzz corpus holds
				//
				//	"$(<<'E'\ncase x in x) `:>ran`E\nE\ncase x in x) "
				//
				// which bash runs. But the doubt was stated about any ), and so
				// stated it cost more than every other reading in this file put
				// together: guardscan over this machine's transcripts put the
				// literal-backtick rule at 5 050 refusals of 554 998 commands,
				// and the top of that table was one shape — a PR body or commit
				// message passed as `--body "$(cat <<'EOF' … EOF\n)"` whose prose
				// holds an ordinary pair of parentheses, as prose does. From that
				// ) the whole command read as code, so every backtick after it
				// read as live, in the very form this guard's own advice
				// prescribes for prose.
				//
				// ⛔ WHAT SEPARATES THEM IS BALANCE, AND THE SHELLS WERE ASKED.
				// A ) the body itself opens with a ( does not end anything in
				// zsh, bash or dash; a ) with no ( of its own can. So the doubt
				// now covers exactly the unbalanced case, plus every expanded
				// body, where the shell also substitutes what it reads and this
				// lexer's model of it is weaker.
				//
				// ⛔ WHICH BASH ANSWERS MATTERS, so neither half may be inferred
				// from the shells that happen to be installed: the unbalanced
				// cases are evaluated by bash 3.2.57, the one Apple ships, and
				// NOT by the bash on ubuntu-latest. A guard refuses what ANY
				// shell would run, so an absence here is not a verdict.
				// See TestTheRuleAllowsWhatNoRealShellRuns in literaltick, which
				// states each case's expectation and puts it to zsh, bash and
				// dash rather than reading the expectation off them.
				if len(stack) > 0 && (expanded && strings.Contains(s[from:i], ")") ||
					unbalancedClose(s[from:i])) {
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

// unbalancedClose reports whether body holds a ) that body itself does not
// open — the one a command substitution around it can end on.
//
// ⛔ IT MUST COUNT A ( ONLY WHERE BASH COUNTS ONE, and the first version of
// this function did not. bash finds the ) that closes a $( … ) by scanning the
// characters, and that scan SKIPS quoted spans — '…', "…", `…`, $'…' — and a #
// comment. A ( inside one of those is a ( bash never sees, so counting it here
// made the following ) look matched while bash treated it as the end of the
// substitution. Everything after it was then live code that this package
// called a quoted heredoc body: a FALSE ALLOW, and the inputs were ordinary
// English prose, because an apostrophe opens a span:
//
//	--body "$(cat <<'EOF'
//	don't do this (it's wrong) and run `…` instead
//	EOF
//	)"
//
// bash 3.2.57 runs that backtick. "don'" … "(it'" is one span, the ( is hidden
// inside it, and the ) after "s wrong" ends the substitution. Found by review,
// not by this package's own tests, whose rows all happened to be quote-free.
//
// ⛔ WHAT THIS MODELS, NAMED SO THE NEXT READER CAN DIFF AGAINST IT: bash's
// character scan for the ) that closes a $( … ), which runs BEFORE the command
// inside is parsed. That is a different routine from the parser, and therefore
// from [Classify], which models what a byte MEANS once parsed. Deriving this
// function from Classify was tried and is wrong in two witnessed ways: Classify
// uses a wider word-start set for a #, and it knows about heredocs, so a PR
// body that merely TALKS about a heredoc has its ) classed as body text while
// bash's scan — which has no notion of heredocs — ends the substitution there.
// See TestAHeredocInTheProseIsNotAHeredoc.
//
// In bash 3.2 that scan is reported to be subst.c's extract_delimited_string
// (named from an independent review, not read here); bash 5 replaced it with
// the real parser, which is why bash 5 does not reproduce any of this and why
// CI cannot judge these cases. The authority is therefore the shells on the
// machine, asked per syntax variant, in both directions.
//
// ⛔ AN UNTERMINATED SPAN ENDS NOTHING, and that is measured rather than
// assumed. A scan that cannot close a span never reaches a ) either, so it
// cannot end the substitution early: bash 3.2.57 answers `bash -n` on the real
// PR body of this package's testdata with
//
//	unexpected EOF while looking for matching `''
//
// — a syntax error, in which nothing runs at all — while zsh -n accepts the
// same file, because zsh does not scan a quoted heredoc body for quotes. So an
// unterminated span is an allow here, not the unsure case. Ordinary prose has
// an odd apostrophe more often than not, and refusing on it would put back
// most of the 5 050.
func unbalancedClose(body string) bool {
	depth := 0
	for i := 0; i < len(body); i++ {
		switch c := body[i]; c {
		case '\\':
			i++ // a \) is not a ) to anyone
		case '\'', '"', '`':
			// Skip the span, the way bash's scan does. A backtick span counts
			// too: it is what keeps the apostrophes of ordinary prose from
			// pairing across a code fence.
			//
			// ⛔ $'…' HONOURS A BACKSLASH AND '…' DOES NOT, so the two end in
			// different places and a ) can be quoted in one and bare in the
			// other. Skipping both alike closed $'a\'' early at the \', left
			// the trailing ' looking unterminated — an allow — and bash 3.2.57
			// then ended the substitution at the ) after it and ran the rest.
			// A false allow, and a regression of the hidden-paren fix; found by
			// the second pass of the review that found the first. The control
			// that isolates it: without the $, bash does NOT end it, because
			// there the backslash is literal and the span really does stop at
			// the \'. See TestAnsiCQuotingEndsWhereBashEndsIt.
			// ⛔ AND THE $ MUST NOT ITSELF BE ESCAPED. A \$ is a literal
			// dollar, so the quote after it opens a PLAIN span, where a
			// backslash is literal and the span ends at the next quote. Read
			// as ANSI-C it swallowed that quote and the rest looked
			// unterminated — an allow — while bash ended the substitution at
			// the ) after it. Found by [FuzzBody] in 71 seconds with the body
			//
			//	\$'\')`…`
			//
			// which is the fourth false allow in this one reading, and the
			// first that a fuzzer rather than a reviewer found: fixing the
			// frame is what let the search reach this function at all.
			ansiC := c == '\'' && i > 0 && body[i-1] == '$' && !oddBackslashes(body, i-1)
			j := i + 1
			for ; j < len(body); j++ {
				if body[j] == '\\' && (c != '\'' || ansiC) {
					j++
					continue
				}
				// ⛔ A BACKTICK INSIDE " … " OWNS THE NEXT QUOTE (#133): bash's
				// scan reads `…` as a span of its own there, so a " inside it
				// does not close the outer one. ``"`"`")`T`` is the outer "
				// closing on the THIRD ", leaving the ) bare; closing it on the
				// second hid the ) inside a backtick span and allowed what bash
				// 3.2.57 runs.
				if c == '"' && body[j] == '`' {
					k := j + 1
					for ; k < len(body) && body[k] != '`'; k++ {
						if body[k] == '\\' {
							k++
						}
					}
					if k >= len(body) {
						return false // an unterminated span: the scan never reaches a )
					}
					j = k
					continue
				}
				if body[j] == c {
					break
				}
			}
			if j >= len(body) {
				return false // unterminated: the scan never reaches a )
			}
			i = j
		case '#':
			// ⛔ WHITESPACE OR A NEWLINE, AND NOTHING ELSE — which is NARROWER
			// than [wordStart]'s set and was measured, not reasoned. bash was
			// asked where a # hides a ( from its own scan
			// (TestWhereACommentHidesAParenFromBash):
			//
			//	"# a ( note"   after \n   comment      bash ended the $( … )
			//	"; #a ( note"  after ' '  comment      ended it
			//	"x #a ( note"  after ' '  comment      ended it
			//	";#a ( note"   after ;    NOT a comment   did not end it
			//	"|#a ( note"   after |    NOT a comment   did not end it
			//	"x#a ( note"   mid-word  NOT a comment   did not end it
			//
			// The ; and | rows are why this is not wordStart: a fuzz run found
			// ";#case x in x) `…`" in 44 seconds, where treating that # as a
			// comment hid the ) and allowed what bash runs.
			if i > 0 && strings.IndexByte(" \t\n", body[i-1]) < 0 {
				continue
			}
			j := strings.IndexByte(body[i:], '\n')
			end := i + j
			if j < 0 {
				end = len(body)
			}
			// ⛔ A COMMENT WITH A ) AND A " SPAN THAT BREAKS THE SCAN (#133, found
			// by FuzzBody). Asked of bash 3.2.57 per variant, on
			// whether the substitution still spans the whole body and whether a
			// backtick after it runs (TestACommentWithAParenAndAQuote):
			//
			//	"# ) note"             spans: the comment hides the )
			//	"# ) \"x\"|T"          spans, nothing ran: a plain word in " "
			//	"# \"a b\" )|T"        spans: the " BEFORE the ) changes nothing
			//	"# ) \"a b\""          does NOT span (a space), "# ) \"a|b\"|T" RAN
			//	"# ) \"a&b\"|T"        RAN; "#)\"|T" RAN; "#)\"|\"T" RAN
			//	"# ) \"a$b\"", "# ) \"a.b-c_d\"" span; ( ) ; ' ` , tab and a
			//	                       newline inside the " " do not
			//
			// WHAT THE CODE DOES, which is wider than the rows above and was
			// chosen so: when the comment LINE holds a ), [quoteSpanBreaks] pairs
			// the " characters from the # to the END OF THE BODY (not to the end of
			// the comment line, and not from the )), and the body is unsure if a
			// pair encloses any of | & newline ' \ or a backtick, or a # that
			// starts a comment (after ; & ( or a blank), or if a " is left
			// unpaired. The # is FuzzBody's (#133): after the cut the " of
			// `";#"` closes the command's own quote, the ; ends the command and
			// the # starts a comment, so the next line is code (bash 3.2.57 ran
			// `#)";#"` newline T; the same without the #, or with `a#b`, ran
			// nothing). A span of plain text is not unsure, nor is a span
			// holding a space, ( ) ; or a comma, although bash cuts some of
			// those: refusing them cost more than it bought, and no cut among
			// them ran a backtick. A conjunction rule (a ) in the comment, a "
			// after it, a lone | after that) was tried instead of the | & ' \
			// and backtick tests and cost MORE (594 against 586) and misses the
			// # case, which runs with no pipe. Limiting the scan to the comment line
			// caught 1 cut of the 29 measured, so the scan runs on. Any ) with any
			// quote cost 755 refusals and any paren 1 051, against 551 before.
			// The spans are paired from the # itself, not from the ): FuzzBody
			// found `#")")"|T`, where the first ) sits inside a span.
			if strings.IndexByte(body[i:end], ')') >= 0 && quoteSpanBreaks(body[i:]) {
				return true
			}
			// ⛔ AN ODD NUMBER OF " ON THE COMMENT LINE (#133, "V2"): the lone "
			// opens a span in bash's scan that runs into the following lines and
			// pairs the quotes there differently from this scan. With a ) anywhere
			// later, `#"` newline `")"|T` ran its backtick in bash 3.2.57. Unsure
			// refuses; it cost +2 refusals over the transcripts.
			if strings.Count(body[i:end], `"`)%2 == 1 && strings.IndexByte(body[end:], ')') >= 0 {
				return true
			}
			// The same for ' and `, found by FuzzBody (`#'` newline `')"|T`
			// ran in bash 3.2.57): the odd quote of the comment pairs with the
			// next one of its kind, and a ) AFTER that one is bare. Prose with
			// an apostrophe is common, so the rule also needs a " and a |
			// after that ): any ) after an odd ' cost 745 refusals against 581,
			// with the backtick as well 746.
			for _, q := range []string{"'"} {
				if strings.Count(body[i:end], q)%2 == 1 {
					if k := strings.Index(body[end:], q); k >= 0 && closeQuotePipe(body[end+k+1:]) {
						return true
					}
				}
			}
			if j < 0 {
				return false // the comment runs to the end: no ) can be seen
			}
			i += j
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return true
			}
			depth--
		}
	}
	return false
}

// quoteSpanBreaks pairs the " characters of rest, which runs from the # of a
// comment to the END OF THE BODY, and reports whether a pair encloses any of
// | & newline ' \ a backtick or a comment-starting #, or a " is left without a
// partner: the shape in which bash 3.2.57's scan stops hiding the ) of the comment (#133).
func quoteSpanBreaks(rest string) bool {
	for {
		open := strings.IndexByte(rest, '"')
		if open < 0 {
			return false
		}
		rest = rest[open+1:]
		shut := strings.IndexByte(rest, '"')
		if shut < 0 {
			return true
		}
		if strings.ContainsAny(rest[:shut], "|&\n'\\`") || commentStartIn(rest[:shut]) {
			return true
		}
		rest = rest[shut+1:]
	}
}

// closeQuotePipe reports whether rest holds a ), then a ", then a | : the
// conjunction in which a ) cut by a paired ' span ran its backtick.
func closeQuotePipe(rest string) bool {
	p := strings.IndexByte(rest, ')')
	if p < 0 {
		return false
	}
	q := strings.IndexByte(rest[p:], '"')
	if q < 0 {
		return false
	}
	return strings.IndexByte(rest[p+q:], '|') >= 0
}

// commentStartIn reports whether s holds a # that starts a comment once the
// quotes around s are read the other way: one after a ; & ( blank or tab.
func commentStartIn(s string) bool {
	for k := 1; k < len(s); k++ {
		if s[k] == '#' && strings.IndexByte(";&( \t", s[k-1]) >= 0 {
			return true
		}
	}
	return false
}

// oddBackslashes reports whether the byte at k is preceded by an odd number of
// backslashes, which makes it escaped and therefore literal.
func oddBackslashes(s string, k int) bool {
	n := 0
	for j := k - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
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
	// ⛔ A # at the start of the line that FOLLOWS a heredoc's terminator is a
	// comment too (fuzz, #131: `<<'E'`+nl+`E`+nl+`#'E`+nl+T+`E`+nl+`'` runs T
	// in bash): the newline ending the terminator line is heredoc class, not
	// code, and reading that # as an ordinary byte let the ' after it quote T.
	if i > 0 && s[i-1] == '\n' && (cls[i-1] == ClassHeredocQuoted || cls[i-1] == ClassHeredocExpanded) {
		return true
	}
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
