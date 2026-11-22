// Package sshweak refuses an ssh, scp, sftp or rsync command line that weakens
// what ssh checks: the host's key, or how the user proves who they are.
//
// ⛔ Measured 2026-11-22: an agent ran `ssh -o StrictHostKeyChecking=no -o
// UserKnownHostsFile=/dev/null user@cfarm433.cfarm.net ...`. The host was
// unverified (its key differed from known_hosts), it rejected our key, and ssh
// fell back to a password prompt, with the agent forwarded to it. A `-o` on the
// command line overrides ~/.ssh/config, so no config file can forbid this; only
// a parse of the command can. Keys only: a changed or unknown host key means
// STOP and report to the user.
package sshweak

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-gitsafe/gitsafe/waiveenv"
)

// Advice is appended to every refusal.
const Advice = `An unknown or CHANGED host key means STOP and report it to the user: do not
turn the check off, point known_hosts at /dev/null, or fall back to a password.
Authentication here is by key only, and the agent is forwarded to no host. If
ssh refuses, say what it printed; the user decides.`

// Finding is one weakening.
type Finding struct {
	Option string // the option, program or variable, as named
	Match  string // the text that matched
}

// Found reports whether a weakening was found.
func (f Finding) Found() bool { return f.Option != "" }

// Why is the reason, in a sentence.
func (f Finding) Why() string {
	return "it weakens ssh's host-key or authentication checks (" + f.Option + ")"
}

// String points at the text.
func (f Finding) String() string { return fmt.Sprintf("It matched `%s`", f.Match) }

var sshFamily = map[string]bool{"ssh": true, "scp": true, "sftp": true, "rsync": true}

var wrappers = map[string]bool{
	"sudo": true, "env": true, "nohup": true, "command": true, "exec": true,
	"time": true, "nice": true, "timeout": true, "doas": true, "setsid": true,
	"xargs": true, "stdbuf": true, "ionice": true, "then": true, "do": true,
	"else": true, "elif": true, "if": true, "while": true, "until": true, "!": true, "{": true,
}

var shells = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true}

// noArgFlags are ssh's single-letter flags that take no argument, so a cluster
// such as -vAN can be read up to the A without mistaking `-lAlice` for it.
const noArgFlags = "46AaCfGgKkMNnqsTtVvXxYy"

// Check parses cmd and returns the first weakening, or a zero Finding.
func Check(cmd string) Finding {
	for _, ws := range waiveenv.Commands(cmd) {
		if f := scan(ws); f.Found() {
			return f
		}
	}
	return Finding{}
}

func scan(ws []string) Finding {
	for i := 0; i < len(ws); i++ {
		w := ws[i]
		if name, _, ok := strings.Cut(w, "="); ok && isIdent(name) {
			if name == "SSH_ASKPASS" || name == "SSH_ASKPASS_REQUIRE" {
				return Finding{Option: name, Match: w}
			}
			continue
		}
		base := filepath.Base(w)
		switch {
		case base == "export" || base == "declare" || base == "typeset" || base == "readonly" || base == "local":
			for _, r := range ws[i+1:] {
				if name, _, ok := strings.Cut(r, "="); ok && (name == "SSH_ASKPASS" || name == "SSH_ASKPASS_REQUIRE") {
					return Finding{Option: name, Match: r}
				}
			}
			return Finding{}
		case base == "sshpass":
			return Finding{Option: "sshpass", Match: strings.Join(ws[i:], " ")}
		case base == "eval":
			return Check(strings.Join(ws[i+1:], " "))
		case shells[base]:
			for _, r := range ws[i+1:] {
				if strings.ContainsAny(r, " \t\n") {
					if f := Check(r); f.Found() {
						return f
					}
				}
			}
			return Finding{}
		case sshFamily[base]:
			return options(base, ws[i+1:])
		case wrappers[base] || strings.HasPrefix(w, "-") || isNumber(w):
			continue
		default:
			return Finding{}
		}
	}
	return Finding{}
}

// options reads the arguments of an ssh-family program. A word holding spaces is
// rsync's `-e "ssh -o ..."` (or a quoted -o value), so it is read as words too.
func options(prog string, args []string) Finding {
	type tok struct {
		s   string
		ssh bool // -A means forward-agent here
	}
	// -A is ssh's forward-agent flag; for rsync it is something else (ACLs),
	// so it counts only for ssh itself or inside an `ssh ...` string.
	var toks []tok
	for _, a := range args {
		toks = append(toks, tok{a, prog == "ssh"})
		if strings.ContainsAny(a, " \t") {
			fs := strings.Fields(a)
			inner := len(fs) > 0 && filepath.Base(fs[0]) == "ssh"
			for _, f := range fs {
				toks = append(toks, tok{f, inner})
			}
		}
	}
	for i := 0; i < len(toks); i++ {
		t, sshHere := toks[i].s, toks[i].ssh
		var opt string
		switch {
		case t == "-o" && i+1 < len(toks):
			opt = toks[i+1].s
			i++
		case strings.HasPrefix(t, "-o") && len(t) > 2:
			opt = t[2:]
		case sshHere && forwardsAgent(t):
			return Finding{Option: "ForwardAgent (-A)", Match: t}
		}
		if opt == "" {
			continue
		}
		if f := weak(opt); f.Found() {
			return f
		}
	}
	return Finding{}
}

func forwardsAgent(t string) bool {
	if !strings.HasPrefix(t, "-") {
		return false
	}
	for _, c := range t[1:] {
		if c == 'A' {
			return true
		}
		if !strings.ContainsRune(noArgFlags, c) {
			return false
		}
	}
	return false
}

// weak judges one `Name=value` or `Name value` option.
func weak(opt string) Finding {
	opt = strings.Trim(opt, `"'`)
	// No separator means a bare name with no value, which no case below flags.
	name, val := opt, ""
	if i := strings.IndexAny(opt, "= \t"); i >= 0 {
		name = strings.Trim(opt[:i], `"'`)
		val = strings.ToLower(strings.Trim(strings.TrimLeft(opt[i:], "= \t"), `"'`))
	}
	bad := false
	switch strings.ToLower(name) {
	case "stricthostkeychecking":
		bad = val == "no" || val == "off" || val == "false" || val == "accept-new"
	case "userknownhostsfile", "globalknownhostsfile":
		bad = strings.Contains(val, "/dev/null")
	case "passwordauthentication", "kbdinteractiveauthentication", "challengeresponseauthentication":
		bad = val == "yes" || val == "true"
	case "preferredauthentications":
		bad = strings.Contains(val, "password") || strings.Contains(val, "keyboard-interactive")
	case "forwardagent":
		bad = val != "" && val != "no" && val != "false"
	}
	if !bad {
		return Finding{}
	}
	return Finding{Option: name, Match: opt}
}

// isIdent: an empty name counts, so a stray `=x` in front is skipped like an
// assignment and the program behind it is still read.
func isIdent(s string) bool {
	for i, c := range s {
		switch {
		case c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

// isNumber: an empty word counts too, for the same reason as in isIdent.
func isNumber(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && c != '.' && c != 's' && c != 'm' && c != 'h' {
			return false
		}
	}
	return true
}
