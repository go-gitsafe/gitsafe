package literaltick

import (
	"path/filepath"
	"strings"

	"github.com/go-gitsafe/gitsafe/plain"
	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// CheckFor is the decision guard-bash takes (#156): the grammar is chosen from the
// shell the command will run under, and a backtick outside that grammar is refused
// by NAME rather than guessed at.
//
//	zsh   S+  ([plain.CheckHeredoc]): measured sound for the zsh that runs commands here
//	bash  S   ([plain.Check]): no heredocs, because bash 3.2's scan reads their body
//	other refused: no grammar was measured for it
//
// shell is the value of $SHELL (a path or a name). A command with no backtick is
// never refused. The escape prefix of [Escape] allows its own statement, as in
// [Check]; on a command the grammar cannot read it needs EVERY backtick covered.
//
// ⛔ [Check], the older reading built on [shellcmd.Classify], is kept for the tests
// and for guardscan's before/after: it modelled the parse of bash 3.2.57, which
// never executes a command from this guard, and chased it through #124, #133,
// #136, #146 and #151.
func CheckFor(shell, cmd string) Finding {
	if !strings.Contains(cmd, "`") {
		return Finding{}
	}
	name := strings.TrimPrefix(filepath.Base(strings.TrimSpace(shell)), "-") // a login shell is "-zsh"
	var res plain.Result
	switch name {
	case "zsh":
		res = plain.CheckHeredoc(cmd)
	case "bash":
		res = plain.Check(cmd)
	default:
		if name == "" || name == "." {
			name = "unset"
		}
		res = plain.Result{V: plain.Uncertain, Reason: "$SHELL is " + name + ": only zsh and bash have a measured grammar"}
	}
	switch res.V {
	case plain.NoTick, plain.Allow:
		return Finding{}
	}
	cls := shellcmd.Classify(cmd)
	first := strings.IndexByte(cmd, '`')
	if res.V == plain.Refuse {
		// A backtick the grammar says is evaluated. The escape covers it only when
		// every backtick the older reading evaluates carries the prefix, and there
		// is at least one.
		return refusedAt(cmd, cls, first)
	}
	// Uncertain: nothing about the classes can be trusted, so the escape must
	// cover every backtick there is.
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == '`' && !escapedByPrefix(cmd, cls, i) {
			return Finding{Off: i, Context: window(cmd, i), Construct: res.Reason}
		}
	}
	return Finding{}
}

// refusedAt judges a command the grammar refused, by the classes of the older
// reading. It is a function of its own so that the case no known command
// reaches -- the grammar refuses, yet the older reading finds no evaluated
// backtick at all -- can be driven with classes written by hand. That case
// refuses at the first backtick: the grammar's word stands.
func refusedAt(cmd string, cls []byte, first int) Finding {
	off, evaluated := -1, 0
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '`' {
			continue
		}
		switch cls[i] {
		case shellcmd.ClassSingle, shellcmd.ClassComment,
			shellcmd.ClassHeredocQuoted, shellcmd.ClassEscaped:
			continue
		}
		evaluated++
		if !escapedByPrefix(cmd, cls, i) && off < 0 {
			off = i
		}
	}
	if evaluated > 0 && off < 0 {
		return Finding{}
	}
	if off < 0 {
		off = first
	}
	return Finding{Off: off, Context: window(cmd, off), InDoubleQuotes: cls[off] == shellcmd.ClassDouble}
}
