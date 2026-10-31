package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Corpus is what the transcripts say this machine actually runs: each distinct
// Bash command, how often it appears, and whether it was ever run with a
// GITSAFE_ALLOW_* prefix.
type Corpus struct {
	Files    int
	Lines    int
	Total    int
	Commands map[string]int
	Escaped  map[string]int
}

// Read walks dir for Claude Code transcripts and collects every Bash command
// in them.
//
// ⛔ IT NEVER RUNS ANYTHING. Every rule in this repository is a pure function
// of the command TEXT, which is what makes this corpus usable at all: ten
// thousand commands can be put to the guard without one of them touching a
// repository, a token or the network.
//
// ⛔ AND IT MUST NOT READ A TRANSCRIPT INTO MEMORY. These files run to
// gigabytes, so each is scanned line by line with a large buffer -- one JSON
// object per line -- and only the commands are kept.
func Read(dir string, maxLine int) (*Corpus, error) {
	c := &Corpus{Commands: map[string]int{}, Escaped: map[string]int{}}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// A directory this user cannot read is not a reason to report
			// nothing about the ones they can.
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		c.Files++
		return c.readFile(path, maxLine)
	})
	return c, err
}

func (c *Corpus) readFile(path string, maxLine int) error {
	f, err := os.Open(path)
	if err != nil {
		return nil // unreadable transcript: skipped, counted in Files
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	for sc.Scan() {
		c.Lines++
		var doc any
		if json.Unmarshal(sc.Bytes(), &doc) != nil {
			continue // a truncated last line is normal in a live transcript
		}
		for _, cmd := range bashCommands(doc) {
			c.Total++
			c.Commands[cmd]++
			if name, ok := escapePrefix(cmd); ok {
				c.Escaped[name]++
			}
		}
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return nil // a single unreadable line is not a failure of the walk
	}
	return nil
}

// bashCommands walks a decoded transcript line for anything that names the
// Bash tool and carries a command.
//
// ⛔ IT LOOKS FOR THE SHAPE, NOT A PATH THROUGH THE DOCUMENT, and both
// spellings of it. The transcript format is not ours to pin: a tool call
// appears as {"name":"Bash","input":{"command":…}} and the hook payload the
// guard itself receives as {"tool_name":"Bash","tool_input":{"command":…}}. A
// reader keyed to one position in one version of one format would go quiet at
// the next change -- and a corpus that reads zero commands reports a clean
// machine.
func bashCommands(doc any) []string {
	var out []string
	switch v := doc.(type) {
	case map[string]any:
		if isBash(v["name"]) || isBash(v["tool_name"]) {
			for _, key := range []string{"input", "tool_input"} {
				if in, ok := v[key].(map[string]any); ok {
					if cmd, ok := in["command"].(string); ok && strings.TrimSpace(cmd) != "" {
						out = append(out, cmd)
					}
				}
			}
		}
		for _, child := range v {
			out = append(out, bashCommands(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, bashCommands(child)...)
		}
	}
	return out
}

func isBash(v any) bool {
	s, ok := v.(string)
	return ok && s == "Bash"
}

// escapePrefix names the GITSAFE_ALLOW_* assignment a command begins with, if
// any.
//
// ⭐ EVERY ONE OF THESE IS A PLACE SOMEBODY BELIEVED THE GUARD WAS WRONG. That
// makes them the most valuable rows in the corpus: a refusal somebody worked
// around is a refusal somebody judged illegitimate, at the moment they were
// holding the problem. Reaching for the escape should be rare, and each time
// it is not, the rule that provoked it is the thing to look at.
func escapePrefix(cmd string) (string, bool) {
	for _, f := range strings.Fields(cmd) {
		name, value, ok := strings.Cut(f, "=")
		if !ok || strings.HasPrefix(f, "-") {
			return "", false
		}
		if strings.HasPrefix(name, "GITSAFE_ALLOW_") && value != "" && value != "0" {
			return name, true
		}
	}
	return "", false
}
