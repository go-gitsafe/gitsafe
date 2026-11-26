package secretread

import (
	"path/filepath"
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// displayers are the programs that copy a file's content to their output.
// `ssh host cat file` is out of scope: the file is not on this machine to open.
var displayers = map[string]bool{
	"cat": true, "head": true, "tail": true, "less": true, "more": true, "bat": true,
	"batcat": true, "nl": true, "tac": true, "rev": true, "cut": true, "sort": true,
	"uniq": true, "paste": true, "column": true, "fold": true, "strings": true,
	"xxd": true, "od": true, "hexdump": true, "base64": true, "jq": true, "yq": true,
	"tomlq": true, "xq": true, "diff": true, "sdiff": true, "colordiff": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "sed": true,
	"awk": true, "gawk": true, "mawk": true, "tr": true, "echo": false, "view": true,
	"vi": true, "vim": true, "nano": true, "open": false, "cp": false,
}

// scripted programs take a script/pattern/filter as first non-flag argument,
// which is not a file.
var scripted = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "sed": true,
	"awk": true, "gawk": true, "mawk": true, "jq": true, "yq": true, "tomlq": true,
	"xq": true, "tr": true,
}

// wrappers run the next word as the program.
var wrappers = map[string]bool{
	"env": true, "sudo": true, "command": true, "builtin": true, "time": true,
	"nice": true, "nohup": true, "exec": true,
}

// Operands returns the file operands that cmd would display, un-expanded, in
// order. Operands that do not name a file are returned too (a pattern, a
// number): the caller opens each and skips what does not exist, so listing a
// word too many costs nothing and listing one too few is a leak.
func Operands(cmd string) []string {
	var out []string
	for _, st := range shellcmd.Statements(cmd) {
		for _, stage := range shellcmd.Stages(st) {
			out = append(out, stageOperands(stage)...)
		}
	}
	return out
}

func stageOperands(stage string) []string {
	ws := Words(stage)
	var files []string
	// `cmd < file` reads the file whatever cmd is.
	for i, w := range ws {
		if w == "<" && i+1 < len(ws) {
			files = append(files, ws[i+1])
		} else if len(w) > 1 && w[0] == '<' && w[1] != '<' && w[1] != '(' {
			files = append(files, w[1:])
		}
	}
	i := 0
	for i < len(ws) {
		w := ws[i]
		if isAssign(w) || wrappers[filepath.Base(w)] {
			i++
			continue
		}
		break
	}
	if i >= len(ws) {
		return files
	}
	prog := filepath.Base(ws[i])
	if !displayers[prog] {
		return files
	}
	var args []string
	skip := false
	for _, w := range ws[i+1:] {
		if skip {
			skip = false
			continue
		}
		if w == "<" || w == ">" || w == ">>" || w == "2>" || w == "&>" {
			skip = true
			continue
		}
		if strings.HasPrefix(w, ">") || strings.HasPrefix(w, "2>") || strings.HasPrefix(w, "&>") ||
			strings.HasPrefix(w, "<") {
			continue
		}
		args = append(args, w)
	}
	var flags string
	var plain []string
	haveScript := false
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			flags += " " + a
			if a == "--regexp" || a == "--file" || strings.HasPrefix(a, "--regexp=") || strings.HasPrefix(a, "--file=") {
				haveScript = true
			}
			continue
		}
		if len(a) > 1 && a[0] == '-' {
			flags += a[1:]
			if strings.ContainsAny(a[1:], "ef") && prog != "head" && prog != "tail" {
				haveScript = true
			}
			continue
		}
		plain = append(plain, a)
	}
	if prog == "sed" && strings.Contains(flags, "i") && !strings.Contains(flags, "--") {
		return files // in place: prints nothing
	}
	if prog == "grep" || prog == "egrep" || prog == "fgrep" || prog == "rg" {
		if quietGrep(flags) {
			return files
		}
	}
	if scripted[prog] && !haveScript && len(plain) > 0 {
		plain = plain[1:]
	}
	return append(files, plain...)
}

// quietGrep is true when the options make grep print no file content.
func quietGrep(flags string) bool {
	for _, long := range []string{"--count", "--quiet", "--silent", "--files-with-matches",
		"--files-without-match", "--list-files"} {
		if strings.Contains(flags, long) {
			return true
		}
	}
	short := flags
	if i := strings.Index(short, " --"); i >= 0 {
		short = short[:i]
	}
	return strings.ContainsAny(short, "cqlL")
}

func isAssign(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for _, r := range w[:i] {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// Words splits one shell stage into words, honouring '…', "…" and backslash.
// It does not expand anything.
func Words(s string) []string {
	var out []string
	var cur strings.Builder
	in := false
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			} else if c == '\\' && q == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			q, in = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

// CheckCommand checks every file cmd would display and returns the first
// refused verdict, plus the verdicts that could not be read (for stderr).
func CheckCommand(cmd, cwd, home string) (refused *Verdict, unreadable []Verdict) {
	n := 0
	for _, op := range Operands(cmd) {
		if strings.ContainsAny(op, "`") || strings.Contains(op, "$(") || op == "" || op == "-" {
			continue
		}
		p := Expand(op, home, cwd)
		cands := []string{p}
		if strings.ContainsAny(p, "*?[") {
			if m, err := filepath.Glob(p); err == nil {
				cands = m
			}
		}
		for _, c := range cands {
			if n++; n > 64 {
				return nil, unreadable
			}
			v := CheckFile(c, home)
			if v.Err != nil {
				unreadable = append(unreadable, v)
			}
			if v.Refused() {
				return &v, unreadable
			}
			if why := SensitivePath(c, home); why != "" && isDir(c) {
				v.ByPath = why
				return &v, unreadable
			}
		}
	}
	return nil, unreadable
}
