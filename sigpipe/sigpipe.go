// Package sigpipe answers one question: would this pipeline KILL the command
// whose work matters, before it finished?
//
// # Why it exists
//
// A consumer that stops reading closes the pipe, and the producer dies of
// SIGPIPE wherever it had got to. So this does not truncate a report — it
// abandons the work:
//
//	timeout 400 go mod tidy 2>&1 | head -5
//
// On 2026-09-06 that was run against a third party's repository,
// `Polytechnique-IDCS/dashboard#20`, and read as a defect in THEIR module: the
// conclusion written down was "tidy failed". Re-run without the pipe it exited
// 0, and `go build` and `go vet` were green afterwards. `head` had returned
// after five lines and killed `tidy` in the middle of its downloads. Nothing had
// ever been wrong.
//
// That is the shape this refuses. It is the SIBLING of [pipestatus], and the two
// damages are separable: pipestatus is about reading a status that belongs to
// the wrong command, and this is about the producer never reaching the end of
// its work. A pipeline can do either, both, or neither.
//
//	go build ./... | tail -5 ; echo $?   # pipestatus: a wrong status, work FINISHED
//	go mod tidy    | head -5             # here: no status read, work ABANDONED
//
// # What it refuses, stated as the ACT
//
// Feeding a command whose completion matters into a consumer that stops reading
// before the input ends.
//
// The consumer half is decidable and is not a list of names to keep up with: it
// is a question about whether the program can return early.
//
//	head            returns after N lines or bytes, by definition
//	grep -q         returns at the FIRST match
//	grep -m N       returns at the Nth
//
// and these do NOT qualify, because each one reads to the end before it can
// answer:
//
//	tail            needs the end to know what the end is
//	wc, sort, uniq  need everything
//	grep, grep -c   reads every line
//	sed 's/…/…/'    streams to the end
//
// # What it does NOT refuse, and that half is the point
//
//	cat f | head -5                 # nothing is abandoned: reading a file again is free
//	git log | head -20              # read-only producer
//	go list ./... | head            # read-only producer
//	go build ./... | tail -5        # tail reads to the end; the build finishes
//	go test ./... | grep -c PASS    # so does grep -c
//	go mod tidy > /tmp/t 2>&1       # the correct form, and the advice
//	echo 'go mod tidy | head -5'    # writing about the form is not the form
//
// # The escape
//
//	GITSAFE_ALLOW_SIGPIPE=1
//
// in front of ONE command, which is an act rather than a setting. There is a
// legitimate use: a producer deliberately run forever and sampled, such as a log
// follow piped into head. That is rare enough to be worth saying out loud.
//
// # What it deliberately cannot cover, and why that is a list
//
// The producer half is NOT decidable from the text. "Does this command's
// completion matter?" is a question about what the program does, so it is
// answered here by a NAMED LIST — the build, fetch and install commands this
// fleet runs. A list inherits the blind spot of its own taxonomy: a producer
// nobody thought of is allowed through, and the first symptom will again be a
// wrong conclusion about somebody else's repository rather than an error.
//
// That is the honest trade. The alternative — refusing every `| head` — would
// refuse `cat f | head` fifty times a day and be worked around by lunchtime,
// which protects nothing. See the allow table in the test, which is the longer
// half on purpose.
package sigpipe

import (
	"strings"

	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// Finding is why a command was refused. The zero value means nothing was found.
type Finding struct {
	// Producer is the stage that would be killed before it finished.
	Producer string
	// Consumer is the stage that stops reading.
	Consumer string
	// Pipeline is the whole statement, so a refusal can point at something.
	Pipeline string
}

// Advice is what to do instead. A guard that says no without saying what to do
// gets worked around.
const Advice = "Let it finish and read the file afterwards: " +
	"`cmd > /tmp/out.txt 2>&1; head -5 /tmp/out.txt` — which also keeps the " +
	"command's own exit status, and keeps the rest of the output for when five " +
	"lines turn out not to be the answer. " +
	"`GITSAFE_ALLOW_SIGPIPE=1` in front of one command is the escape, for a " +
	"producer that is MEANT to be sampled rather than finished."

// Why names the act, for a caller that prints it.
func (f Finding) Why() string {
	return "it would kill the command whose work matters: the consumer stops reading, the pipe closes, and the producer dies of SIGPIPE wherever it had got to"
}

// Found reports whether this finding is a refusal.
func (f Finding) Found() bool { return f.Producer != "" }

func (f Finding) String() string {
	if !f.Found() {
		return ""
	}
	return "`" + f.Consumer + "` returns before its input ends, so `" + f.Producer +
		"` is killed part-way through and whatever it printed first reads as its whole answer"
}

// Check says whether cmd would abandon a producer whose completion matters.
func Check(cmd string) Finding {
	// ⛔ Quoting a forbidden form is not the forbidden form: this package's own
	// documentation, a README and a commit message all have to be able to say
	// what not to write.
	cmd = shellcmd.StripQuotedHeredocs(cmd)
	for _, st := range shellcmd.Statements(cmd) {
		stages := shellcmd.Stages(st)
		if len(stages) < 2 {
			continue
		}
		// The producer is the first stage; a consumer anywhere after it closes
		// the pipe, so `go build 2>&1 | head -20 | sed …` counts.
		prod := strings.TrimSpace(stages[0])
		if quoted(prod) || !abandonsWork(prod) {
			continue
		}
		for _, stage := range stages[1:] {
			if c := strings.TrimSpace(stage); stopsEarly(c) {
				return Finding{
					Producer: prod,
					Consumer: c,
					Pipeline: strings.TrimSpace(st),
				}
			}
		}
	}
	return Finding{}
}

// quoted reports whether a stage is entirely inside quotes — the text of a
// command being written about rather than a command.
func quoted(stage string) bool {
	s := strings.TrimSpace(stage)
	if len(s) < 2 {
		return false
	}
	return (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"')
}

// stopsEarly reports whether a stage can return before its input ends.
//
// This is the decidable half, and it is a question about the program rather than
// a name on a list: `head` is bounded by construction, `grep -q` and `grep -m`
// return at a match, and everything else here has to read to the end before it
// can say anything at all.
// ⛔ THE WRAPPERS HAVE TO BE SKIPPED ON THIS SIDE TOO. The first version of this
// skipped them only on the producer, and the live control caught it at once:
//
//	timeout 400 go mod tidy 2>&1 | command head -5
//
// went straight through, because `command head` reads as the program `command`.
// `command head` is the spelling the operator who wrote this rule types by
// habit, so the guard would have been blind to its own author.
func stopsEarly(stage string) bool {
	w := skipPrefix(words(stage))
	if len(w) == 0 {
		return false
	}
	switch base(w[0]) {
	case "head":
		return true
	case "grep", "egrep", "fgrep", "rg":
		for _, a := range w[1:] {
			if a == "-q" || a == "--quiet" || a == "--silent" ||
				a == "-m" || strings.HasPrefix(a, "--max-count") ||
				strings.HasPrefix(a, "-m") && len(a) > 2 && isDigits(a[2:]) {
				return true
			}
			// A bundled short form such as -iq.
			if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a, 'q') {
				return true
			}
		}
	}
	return false
}

// mutating lists, per command, the subcommands whose completion matters. An
// empty set means every subcommand of that command counts.
//
// ⛔ This is the list the package documentation calls its own blind spot. Each
// entry is a command this fleet actually runs, and a producer absent from it is
// allowed through in silence.
var mutating = map[string]map[string]bool{
	// `go list`, `go env`, `go version` and `go doc` only read, so they are
	// deliberately absent: `go list ./... | head` abandons nothing.
	"go": {
		"build": true, "test": true, "run": true, "install": true,
		"get": true, "mod": true, "work": true, "generate": true,
		"vet": true, "tool": true, "clean": true,
	},
	// `git log`, `git status`, `git diff` and `git show` only read.
	"git": {
		"clone": true, "fetch": true, "pull": true, "submodule": true,
		"gc": true, "repack": true, "lfs": true,
	},
	"docker": {"build": true, "pull": true, "push": true, "buildx": true},
	"cargo":  {"build": true, "install": true, "fetch": true, "test": true},
	"npm":    {"install": true, "ci": true, "run": true, "build": true},
	"pnpm":   {"install": true, "run": true, "build": true},
	"yarn":   {"install": true, "run": true, "build": true},
	"pip":    {"install": true, "download": true},
	"pip3":   {"install": true, "download": true},
	"make":   nil,
	"wget":   nil,
	"rsync":  nil,
}

// abandonsWork reports whether a producer's completion matters.
func abandonsWork(stage string) bool {
	w := words(stage)
	w = skipPrefix(w)
	if len(w) == 0 {
		return false
	}
	cmd := base(w[0])
	subs, ok := mutating[cmd]
	if !ok {
		// curl only writes when it is told to; otherwise it is a read.
		if cmd == "curl" {
			for _, a := range w[1:] {
				if a == "-o" || a == "-O" || a == "--output" || a == "--remote-name" {
					return true
				}
			}
		}
		return false
	}
	if subs == nil {
		return true // every subcommand counts
	}
	for _, a := range w[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return subs[a]
	}
	return false
}

// skipPrefix drops what stands in front of the real command: environment
// assignments and the wrappers this fleet puts there.
//
// ⛔ `timeout 400 go mod tidy | head -5` is the exact command that produced the
// wrong conclusion, so a reader that stopped at `timeout` would miss the very
// case this package was written for.
func skipPrefix(w []string) []string {
	for len(w) > 0 {
		switch {
		case strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "-"):
			w = w[1:] // FOO=bar
		case base(w[0]) == "timeout", base(w[0]) == "gtimeout":
			w = w[1:]
			// Its duration, and any flags before it.
			for len(w) > 0 && (strings.HasPrefix(w[0], "-") || isDuration(w[0])) {
				w = w[1:]
			}
		case base(w[0]) == "command", base(w[0]) == "env", base(w[0]) == "sudo",
			base(w[0]) == "nice", base(w[0]) == "time", base(w[0]) == "stdbuf",
			base(w[0]) == "nohup":
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				w = w[1:]
			}
		default:
			return w
		}
	}
	return w
}

// words splits a stage on whitespace, dropping redirections, which are not
// arguments and must not be read as a subcommand.
func words(stage string) []string {
	var out []string
	for _, f := range strings.Fields(stage) {
		if f == "2>&1" || f == "1>&2" || f == "&>" || strings.HasPrefix(f, ">") ||
			strings.HasPrefix(f, "2>") || strings.HasPrefix(f, "<") {
			continue
		}
		out = append(out, strings.Trim(f, "()"))
	}
	return out
}

// base is the last path element, so `/usr/bin/head` and `head` read alike.
func base(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isDuration recognises timeout's argument: 400, 400s, 1.5h.
func isDuration(s string) bool {
	if s == "" {
		return false
	}
	s = strings.TrimRight(s, "smhd")
	if s == "" {
		return false
	}
	dots := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			dots++
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return dots <= 1
}
