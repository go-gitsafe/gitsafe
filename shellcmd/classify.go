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
		// dollar marks an 'A' frame opened by $(( rather than by (( .
		dollar bool
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
					stack = append(stack, frame{kind: 'A', depth: 2, dollar: true})
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
			//
			// ⛔ NOT EVERY $( IN ARITHMETIC IS THAT DOUBT (#128, #129). The shape
			// `$(( $(wc -c < f) * 2 / 100 ))` is how a script computes with a
			// command's output, and reading everything after it as code made a
			// quoted "=== … ===" line later in the command read as live (62 false
			// matches of eqword over this machine's transcripts). When the
			// arithmetic closes at a matching )) and what lies between holds
			// nothing the shells disagree about, the frame ends there.
			if next == '(' && top() == 'A' && stack[len(stack)-1].dollar {
				if end, ok := nestedArithEnd(s, i, stack[len(stack)-1].depth); ok {
					fill(i, end+1, ClassCode)
					stack = stack[:len(stack)-1]
					i = end + 1
					continue
				}
			}
			for k := i; k < len(s); k++ {
				cls[k] = ClassCode
			}
			return cls
		case c == '$' && next == '(':
			fill(i, i+2, ClassCode)
			if i+2 < len(s) && s[i+2] == '(' {
				cls[i+2] = ClassCode
				stack = append(stack, frame{kind: 'A', depth: 2, dollar: true})
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
				//
				// ⛔ AND A SPAN THE BODY LEAVES OPEN (#136). bash's scan has no
				// notion of a heredoc, so a lone " in a quoted body pairs with the
				// next " ANYWHERE after it, the terminator line included, which
				// then hides the terminator and moves the substitution's close
				// to a later ). `$(<<'E'` NL `"E` NL `E` NL `")"x) #T"` ran T in
				// bash 3.2.57 and in no other shell. Unsure refuses when the
				// body ends inside a span whose quote occurs again after it.
				//
				// ⛔ PRUNED (#156): the balance scan that modelled bash 3.2.57's
				// reading of a QUOTED body inside $( ) (scanClose and its helpers)
				// is gone. Commands here run under zsh, which does not scan a
				// quoted body, and decisions are taken by the grammars of the plain
				// package; what that scan found (#133, #136, #146, #153, #154) is
				// refused by name there.
				if len(stack) > 0 && expanded && strings.Contains(s[from:i], ")") {
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
	// ⛔ #128: the newline that ends a heredoc's terminator line starts a command
	// (asked of bash 3.2.57, dash and zsh: `(`, `(#`, `(|#` after it ran nothing).
	if s[j-1] == '\n' && (cls[j-1] == ClassHeredocQuoted || cls[j-1] == ClassHeredocExpanded) {
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
			return name != "" && assignName.MatchString(name) && (commandPos(s, cls, k) || assignPrefix(s, cls, k))
		}
	}
	// ⛔ #128: the ) that ends a `case` pattern: what follows is a command.
	if j := skipBlanks(s, i); j > 0 && s[j-1] == ')' && cls[j-1] == ClassCode && caseArmClose(s, cls, j-1) {
		return true
	}
	return commandPos(s, cls, i)
}

// assignPrefix reports whether the word that starts at k is preceded only by
// words that keep an array assignment an assignment (#128): declaration
// builtins (local declare typeset export readonly), their -flags, and other
// assignments, the first of them in command position. Asked of bash 3.2.57,
// dash and zsh -f -c: `local a=(…)`, `declare -a a=(…)`, `export a=(…)` and
// `FOO=1 a=(1) …` run nothing after any `#`, `|#` or newline-`#` in the list,
// while `echo a=(…)` is a glob qualifier in zsh and stays unsure.
func assignPrefix(s string, cls []byte, k int) bool {
	p := skipBlanks(s, k)
	if p >= k { // no blank between: the word is not separate
		return false
	}
	w, k2 := wordBefore(s, cls, p)
	switch {
	case w == "":
		return false
	case w == "local" || w == "declare" || w == "typeset" || w == "export" || w == "readonly":
		return commandPos(s, cls, k2)
	case w[0] == '-' && len(w) > 1:
		return assignPrefix(s, cls, k2)
	}
	if eq := strings.IndexByte(w, '='); eq > 0 && assignName.MatchString(strings.TrimSuffix(w[:eq], "+")) {
		return commandPos(s, cls, k2) || assignPrefix(s, cls, k2)
	}
	return false
}

// caseArmClose reports whether the ) at i ends a `case` pattern: the pattern
// (words joined by |) follows `in` of `case WORD in`, or a ;; ;& ;;& (#128).
func caseArmClose(s string, cls []byte, i int) bool {
	w, k := wordBefore(s, cls, i)
	if w == "" {
		return false
	}
	for {
		p := skipBlanks(s, k)
		if p > 0 && s[p-1] == '|' && cls[p-1] == ClassCode {
			w, k = wordBefore(s, cls, skipBlanks(s, p-1))
			if w == "" {
				return false
			}
			continue
		}
		// ⛔ #151: a pattern follows a DOUBLE ;; (or ;& / ;;&), never a single ;.
		// `$(true ;x)(|E`+newline+`#`+T was taken for a case arm because it
		// followed one `;`, so the ( after the ) read as a command start; zsh
		// reads it as a glob group and runs the backtick. Unsure refuses.
		if p > 1 && cls[p-1] == ClassCode && cls[p-2] == ClassCode &&
			(s[p-1] == ';' && s[p-2] == ';' || s[p-1] == '&' && s[p-2] == ';') {
			return true
		}
		kw, _ := wordBefore(s, cls, p)
		return kw == "in" && p < k && commandPos(s, cls, k)
	}
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

// nestedArithEnd answers, for the $( at off inside an open $(( … )) whose paren
// depth is d, where the arithmetic closes: the offset of its final ), and
// whether that is knowable without asking which shell reads it.
//
// ⛔ ASKED OF THE SHELLS (bash 3.2.57, dash, zsh -f -c), per variant, with a
// backtick hidden behind '…' and behind # after the close: for
// `$(( $(cmd) OP n ))`, `$(( n OP $(cmd) ))`, two substitutions, `$(wc -c < f)`
// and a parenthesised `($(cmd))`, NO shell ran the hidden backtick, so the
// arithmetic ends at the )) in all three. The variants that DO differ all hold
// a character this refuses: a # (`$(( $(echo 1) #T))` runs in dash and zsh,
// #128's refuse case), a quote (it runs in all three), a backtick, a
// backslash, a newline, ${ or $[ or a nested $(( , << (a heredoc), a `case`
// (its ) closes nothing), and a close that is not the two adjacent )) at the
// frame's own depth. Anything else here is a plain command and plain operators.
func nestedArithEnd(s string, off, d int) (end int, ok bool) {
	for j := off; j < len(s); j++ {
		var next byte
		if j+1 < len(s) {
			next = s[j+1]
		}
		switch s[j] {
		case '\'', '"', '`', '\\', '#', '\n':
			return 0, false
		case '<':
			if next == '<' {
				return 0, false
			}
		case '$':
			if next == '{' || next == '[' {
				return 0, false
			}
			if next == '(' && j+2 < len(s) && s[j+2] == '(' {
				return 0, false
			}
		case '(':
			d++
		case ')':
			d--
			// ⛔ Same refusal as the main loop: a ) taking the frame to depth 1
			// without a ) after it is not a close; dash reads it as a literal
			// byte and stays in arithmetic, so a later )) is not the close.
			if d == 1 && next != ')' {
				return 0, false
			}
			if d == 0 {
				if s[j-1] != ')' || caseWord.MatchString(s[off:j]) {
					return 0, false
				}
				return j, true
			}
		}
	}
	return 0, false
}
