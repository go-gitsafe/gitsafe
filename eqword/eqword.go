// Package eqword finds, in a command line, a word zsh would take for an EQUALS
// EXPANSION: an unquoted word that begins with `=` followed by a name, which
// zsh replaces with that command's path (`=ls` is /bin/ls).
//
// # Why it is here, and why it only COUNTS
//
// When the lookup fails zsh does not leave the word alone: it reports
// `(eval):1: ==== not found` and ABANDONS THE WHOLE LINE (go-gitsafe/gitsafe
// #129). A decorative `echo ====` between two commands loses every command
// after it, silently, and the lost output reads as an answer.
//
// ⛔ NOTHING IN guard-bash CALLS THIS. literaltick shipped without being put to
// guardscan and refused 5050 of 554 998 real commands; the order here is
// measure first. [Scan] is what guardscan counts; whether to refuse, warn or do
// nothing is decided from those numbers (see the pull request), not from the
// shape of the bug.
//
// # What zsh does, MEASURED (probe_test.go is the table; zsh 5.9)
//
//	abandons the line:  echo ====   echo ==x   echo =nonexistent   echo =1
//	                    a==b (an assignment VALUE is expanded)   case ==== in …
//	                    [[ ==== == ==== ]]  [[ a == ==== ]]   x=(====)
//	                    for i in ====   the LAST stage of a pipeline
//	survives:           echo =   echo =ls (resolves)   echo '===='   echo "===="
//	                    echo \====   a heredoc body   $(echo =nonexistent)
//	                    (echo ====)   echo ==== | cat   echo ==== &   =(cmd)
//	                    setopt no_equals
//
// The survivors are the forms where the expansion happens in a process the
// shell forked: that process dies alone. [Scan] reproduces that split: a word
// inside ( … ), in a pipeline stage that is not the last, or in a job sent to
// the background is not reported, because the line survives it.
package eqword

import (
	"os/exec"
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Lookup says whether name is a command zsh's `=name` would find. It is a
// variable so a test can fix the answer; the real answer depends on PATH, which
// is also what zsh reads.
var Lookup = func(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Hit is one word zsh would equals-expand, in a position the line cannot
// survive, with something after it to lose.
type Hit struct {
	// Off is the byte offset of the word's first byte.
	Off int
	// Word is the word as far as its unquoted part goes.
	Word string
	// Assignment is true when the word is NAME=…: the value is what begins
	// with `=`.
	Assignment bool
	// Resolves is true when the lookup found a command: the expansion works,
	// and nothing is lost. These are the INTENDED uses.
	Resolves bool
	// Lost is a short excerpt of what follows, which the line would not run.
	Lost string
}

// Separator is true when the word is nothing but `=` characters: the
// decorative `====` of a banner.
func (h Hit) Separator() bool {
	return !h.Assignment && strings.Trim(h.Word, "=") == ""
}

// Kind names the category, for a report that groups.
func (h Hit) Kind() string {
	switch {
	case h.Resolves:
		return "an equals expansion that resolves (intended)"
	case h.Assignment:
		return "an assignment value beginning with = that does not resolve"
	case h.Separator():
		return "a run of = (a separator) that does not resolve"
	}
	return "= followed by a name that does not resolve"
}

func (h Hit) String() string {
	return "word " + h.Word + " at " + itoa(h.Off) + ", then: " + h.Lost
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Scan returns every word of cmd that zsh equals-expands in a position that
// takes the whole line down when the lookup fails, and that has a later command
// to lose. Whether each resolves is in the Hit.
func Scan(cmd string) []Hit {
	cls := shellcmd.Classify(cmd)
	var hits []Hit
	depth := 0
	inTest := false
	for i := 0; i < len(cmd); i++ {
		if cls[i] != shellcmd.ClassCode {
			continue
		}
		switch cmd[i] {
		case '(':
			// `x=(…)` is an array assignment the shell runs itself, not a
			// subshell (measured: x=(====) takes the line down). `=(cmd)` is a
			// process substitution and `(`…`)` a subshell: both fork.
			if !(i > 0 && cmd[i-1] == '=' && cls[i-1] == shellcmd.ClassCode &&
				i > 1 && !isDelim(cmd[i-2])) {
				depth++
			}
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if !wordStart(cmd, cls, i) {
			continue
		}
		end := wordEnd(cmd, cls, i)
		word := cmd[i:end]
		switch word {
		case "[[":
			inTest = true
		case "]]":
			inTest = false
		}
		name, assign := expandable(word)
		// ⛔ An assignment is expanded only where the shell reads one: before
		// the command word. `pip install numpy==2.5` has the same text and is
		// an ordinary argument that zsh leaves alone (the first scan counted
		// 270 of those).
		if assign && !beforeCommand(cmd, cls, i) {
			name = ""
		}
		// Inside [[ … ]] the operators == =~ = are operators, not words to
		// expand (measured); any OTHER =word there is still expanded.
		if inTest && (word == "==" || word == "=~" || word == "=") {
			name = ""
		}
		if name == "" {
			i = end - 1
			continue
		}
		if depth == 0 && survives(cmd, cls, end) == "" {
			if lost := lostAfter(cmd, cls, end); lost != "" {
				hits = append(hits, Hit{Off: i, Word: word, Assignment: assign,
					Resolves: Lookup(name), Lost: lost})
			}
		}
		i = end - 1
	}
	return hits
}

// Check returns the hits whose lookup FAILS: the ones that lose the line.
func Check(cmd string) []Hit {
	var out []Hit
	for _, h := range Scan(cmd) {
		if !h.Resolves {
			out = append(out, h)
		}
	}
	return out
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' }

func isDelim(c byte) bool {
	return isSpace(c) || c == ';' || c == '&' || c == '|' || c == '(' || c == ')' ||
		c == '<' || c == '>'
}

// wordStart: i begins a word when the byte before it is a code-class delimiter
// (or nothing).
func wordStart(cmd string, cls []byte, i int) bool {
	if isDelim(cmd[i]) {
		return false
	}
	if i == 0 {
		return true
	}
	return cls[i-1] == shellcmd.ClassCode && isDelim(cmd[i-1])
}

// wordEnd stops at the first delimiter, or at the first byte that is not plain
// code (a quote opens): the name zsh looks up is read up to there.
func wordEnd(cmd string, cls []byte, i int) int {
	j := i
	for j < len(cmd) && cls[j] == shellcmd.ClassCode && !isDelim(cmd[j]) {
		j++
	}
	return j
}

// expandable returns the name zsh would look up, and whether the word is an
// assignment. The empty name means zsh does not expand the word.
func expandable(word string) (name string, assign bool) {
	if len(word) >= 2 && word[0] == '=' {
		if word[1] == '(' {
			return "", false
		}
		return word[1:], false
	}
	// NAME=… : the value is expanded, and `=` at its start is an equals expansion.
	k := 0
	for k < len(word) && (word[k] == '_' || word[k] >= 'a' && word[k] <= 'z' ||
		word[k] >= 'A' && word[k] <= 'Z' || k > 0 && word[k] >= '0' && word[k] <= '9') {
		k++
	}
	if k > 0 && k+2 < len(word)+0 && word[k] == '=' && word[k+1] == '=' {
		return word[k+2:], true
	}
	return "", false
}

// beforeCommand reports whether every word before off in its statement is
// itself an assignment, so that the word at off is read as one.
func beforeCommand(cmd string, cls []byte, off int) bool {
	j := off
	for {
		for j > 0 && (cmd[j-1] == ' ' || cmd[j-1] == '\t') && cls[j-1] == shellcmd.ClassCode {
			j--
		}
		if j == 0 {
			return true
		}
		if cls[j-1] == shellcmd.ClassCode {
			switch cmd[j-1] {
			case ';', '\n', '&', '|', '(', '{':
				return true
			}
		}
		e := j
		for j > 0 && !(cls[j-1] == shellcmd.ClassCode && isDelim(cmd[j-1])) {
			j--
		}
		switch cmd[j:e] {
		case "export", "typeset", "declare", "local", "readonly", "float", "integer":
			return true // their arguments are assignments (measured: export A==b)
		}
		if !isAssignmentWord(cmd[j:e]) {
			return false
		}
	}
}

// isAssignmentWord: NAME=… or NAME+=… with a plain identifier.
func isAssignmentWord(w string) bool {
	k := 0
	for k < len(w) && (w[k] == '_' || w[k] >= 'a' && w[k] <= 'z' ||
		w[k] >= 'A' && w[k] <= 'Z' || k > 0 && w[k] >= '0' && w[k] <= '9') {
		k++
	}
	if k == 0 || k >= len(w) {
		return false
	}
	if w[k] == '+' {
		k++
	}
	return k < len(w) && w[k] == '='
}

// survives returns a non-empty reason when the expansion happens in a process
// the shell forked, so the line outlives the failure: the word is in a
// pipeline stage that is not the last, or in a background job.
func survives(cmd string, cls []byte, from int) string {
	for j := from; j < len(cmd); j++ {
		if cls[j] != shellcmd.ClassCode {
			continue
		}
		switch cmd[j] {
		case ';', '\n':
			return ""
		case '|':
			if j+1 < len(cmd) && cmd[j+1] == '|' {
				return ""
			}
			return "a pipeline stage that is not the last"
		case '&':
			if j+1 < len(cmd) && cmd[j+1] == '&' {
				return ""
			}
			if j > 0 && (cmd[j-1] == '>' || cmd[j-1] == '<') || j+1 < len(cmd) && cmd[j+1] == '>' {
				continue // a redirection such as 2>&1 or &>file
			}
			return "a background job"
		}
	}
	return ""
}

// lostAfter returns a short excerpt of the first later command, or "". A later
// command is what follows a ; newline && || | or & that is more than space and
// a comment.
func lostAfter(cmd string, cls []byte, from int) string {
	seenSep := false
	for j := from; j < len(cmd); j++ {
		if cls[j] == shellcmd.ClassComment {
			continue
		}
		if cmd[j] == '\\' && j+1 < len(cmd) && cmd[j+1] == '\n' {
			j++ // a line continuation joins lines: it is not a separator
			continue
		}
		if cls[j] == shellcmd.ClassCode {
			switch cmd[j] {
			case ';', '\n', '|':
				seenSep = true
				continue
			case '&':
				if j+1 < len(cmd) && cmd[j+1] == '&' {
					seenSep = true
				}
				continue
			case ' ', '\t':
				continue
			}
		}
		if seenSep {
			end := j + 40
			if end > len(cmd) {
				end = len(cmd)
			}
			return strings.ReplaceAll(cmd[j:end], "\n", " ")
		}
	}
	return ""
}
