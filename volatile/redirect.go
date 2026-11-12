package volatile

import "strings"

// CheckRedirect answers the other half of the question: would a command that
// runs IN THE BACKGROUND write its output into a directory this machine
// empties?
//
// ⛔ A background command is long by definition, and its result has to survive
// the turn that started it. On 2026-11-11 `gotest ./interp … >
// /private/tmp/claude-501/<session>/scratchpad/probe.txt 2>&1` ran for 64
// minutes with run_in_background set, and its log was lost when the session
// restarted — the sixth recorded loss to the scratchpad, and the first of a
// plain file rather than a repository.
//
// A FOREGROUND redirect there is still fine, as [Check] says: a one-turn file
// is what the scratchpad is for. The difference is the harness's own
// run_in_background flag, which only the caller can read, so it is a parameter.
//
// It looks at redirections (`>`, `>>`, `2>`, `1>`, `&>`), at the file arguments
// of `tee`, and at the output-file flags `-o`, `-coverprofile`, `-cpuprofile`,
// `-memprofile`, `-blockprofile`, `-mutexprofile`, `-trace` and `-output`
// (`-flag=path` or `-flag path`, single or double dash). A `-o` whose value
// contains `=` is an option, not a file. File descriptor duplication (`2>&1`)
// writes nowhere. Relative paths are resolved against the directory the shell
// is in, moved by `cd`; a tool's own `-C` does not move a redirect.
func CheckRedirect(cmd, cwd, home, tmpdir string, background bool) Finding {
	if !background {
		return Finding{}
	}
	return scan(cmd, cwd, home, redirSegments, func(fields []string, dir string) Finding {
		for _, t := range outputs(fields) {
			dest := resolve(dir, home, expandTmp(t.path, tmpdir))
			if Volatile(dest, tmpdir) {
				return Finding{"redirect", t.match, "a background command's output written to a volatile directory is lost with it, and the command is long by definition", dest}
			}
		}
		return Finding{}
	})
}

// target is a file a command writes, and the text that named it.
type target struct{ path, match string }

// redirSegments is [segments] that does not cut a redirection in two: `&>f` is
// a redirection, and `2>&1` a duplication, not a `&` that ends a command.
func redirSegments(cmd string) []string {
	cmd = strings.ReplaceAll(cmd, "&>>", " >> ")
	cmd = strings.ReplaceAll(cmd, "&>", " > ")
	cmd = strings.ReplaceAll(cmd, ">&", ">\x00")
	return segments(cmd)
}

// outputFlags are the options of go, curl and friends that name an output file.
var outputFlags = map[string]bool{
	"o": true, "coverprofile": true, "cpuprofile": true, "memprofile": true,
	"blockprofile": true, "mutexprofile": true, "trace": true, "output": true,
}

// outputs lists the files one command writes: its redirections, the files of
// `tee`, and its output-file flags.
func outputs(fields []string) []target {
	var out []target
	for i, f := range fields {
		if idx := strings.IndexByte(f, '>'); idx >= 0 && strings.Trim(f[:idx], "0123456789") == "" {
			p, match := strings.TrimLeft(f[idx:], ">"), f
			if p == "" && i+1 < len(fields) {
				p = fields[i+1]
				match = f + " " + p
			}
			if p != "" && p[0] != 0 && p[0] != '(' && p[0] != '>' {
				out = append(out, target{p, match})
			}
			continue
		}
		if i == 0 {
			continue
		}
		flag, value, hasValue := strings.Cut(strings.TrimLeft(f, "-"), "=")
		if !strings.HasPrefix(f, "-") || !outputFlags[flag] {
			continue
		}
		if !hasValue {
			if i+1 >= len(fields) {
				continue
			}
			value = fields[i+1]
		}
		if value != "" && !strings.HasPrefix(value, "-") && !(flag == "o" && strings.Contains(value, "=")) {
			out = append(out, target{value, f + " " + value})
		}
	}
	if base(fields[0]) == "tee" {
		for _, p := range positionals(fields[1:], nil) {
			out = append(out, target{p, "tee " + p})
		}
	}
	return out
}

// expandTmp spells out $TMPDIR, which resolve does not know.
func expandTmp(p, tmpdir string) string {
	if tmpdir == "" {
		return p
	}
	for _, v := range []string{"${TMPDIR}", "$TMPDIR"} {
		if strings.HasPrefix(p, v) {
			return tmpdir + p[len(v):]
		}
	}
	return p
}

// RedirectAdvice is what to do instead, printed with a refusal.
const RedirectAdvice = `A background command is long by definition and its result has to survive the
turn, so it does not write into /tmp, the session scratchpad or $TMPDIR, which
this machine empties (the scratchpad lost a 64-minute log on 2026-11-11). Write
it where it lasts:

    the repository's own directory (ignored by git if need be), or
    ~/Library/Application Support/<tool>/

A foreground command may still write a one-turn file in the scratchpad. If a
throwaway log is really meant, say so for one command:

    GITSAFE_ALLOW_VOLATILE=1 <the command>`
