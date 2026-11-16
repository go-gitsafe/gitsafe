package eqword

import (
	"strings"
	"testing"
)

// fixed makes the lookup deterministic: only these names are commands.
func fixed(t *testing.T, names ...string) {
	t.Helper()
	old := Lookup
	t.Cleanup(func() { Lookup = old })
	Lookup = func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
}

// overCount names the probes where the TEXT cannot tell what the shell did:
// the expansion sits in a branch that is not taken, or an option is set. The
// detector reports them (a count is an upper bound) and the table says why.
var overCount = map[string]string{
	"skipped by and":        "false && … never expands the word",
	"after or when skipped": "true || … never expands the word",
	"no_equals option":      "setopt no_equals turns the expansion off",
}

// TestDetectorAgreesWithTheMeasuredZshTable is the declared test: every row of
// the probe table, which was measured against zsh, is put to Check. A row zsh
// abandons must be found; a row it survives must not, bar the named over-counts.
func TestDetectorAgreesWithTheMeasuredZshTable(t *testing.T) {
	fixed(t, "ls", "true")
	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			tail := p.tail
			if tail == "" {
				tail = "; touch M"
			}
			cmd := p.line + tail
			got := len(Check(cmd)) > 0
			want := p.zshAbandons
			if why, ok := overCount[p.name]; ok {
				if !got {
					t.Errorf("%q: documented over-count (%s) was not found", cmd, why)
				}
				return
			}
			if got != want {
				t.Errorf("Check(%q) found=%v, zsh abandons=%v", cmd, got, want)
			}
		})
	}
}

func TestNothingAfterIsNothingLost(t *testing.T) {
	fixed(t)
	for _, c := range []string{"echo ====", "echo ==== ", "echo ====; ", "echo ==== # tail", "echo ====\n\n"} {
		if h := Check(c); len(h) != 0 {
			t.Errorf("Check(%q) = %v: no later command, nothing is lost", c, h)
		}
	}
}

func TestTheIssueLine(t *testing.T) {
	fixed(t, "git", "grep")
	cmd := "git branch --show-current; git log -1 origin/main; echo ====; \\\n  git ls-tree --name-only origin/main cmd/ > out.txt; grep -n recur out.txt"
	h := Check(cmd)
	if len(h) != 1 || !h[0].Separator() || h[0].Word != "====" {
		t.Fatalf("Check = %v, want one separator ====", h)
	}
	if !strings.HasPrefix(h[0].Lost, "git ls-tree") {
		t.Errorf("Lost = %q, want the lost command", h[0].Lost)
	}
}

func TestKinds(t *testing.T) {
	fixed(t, "ls")
	cases := []struct {
		cmd, kind string
	}{
		{"echo ====; true", "a run of = (a separator) that does not resolve"},
		{"echo =nope; true", "= followed by a name that does not resolve"},
		{"a==b; true", "an assignment value beginning with = that does not resolve"},
		{"echo =ls; true", "an equals expansion that resolves (intended)"},
	}
	for _, c := range cases {
		hs := Scan(c.cmd)
		if len(hs) != 1 || hs[0].Kind() != c.kind {
			t.Errorf("Scan(%q) = %v, want one %q", c.cmd, hs, c.kind)
		}
	}
}

func TestOperatorsInsideDoubleBracketAreNotWords(t *testing.T) {
	fixed(t)
	for _, c := range []string{"[[ a == b ]]; true", "[[ a =~ b ]]; true", "[[ a = b ]]; true"} {
		if h := Scan(c); len(h) != 0 {
			t.Errorf("Scan(%q) = %v", c, h)
		}
	}
	// Outside [[ ]] the same word is expanded: this is the famous `[ a == b ]`.
	if h := Check("[ a == b ]; true"); len(h) != 1 {
		t.Errorf("[ a == b ] should be found, got %v", h)
	}
}

func TestQuotedAndHeredocWordsAreNotFound(t *testing.T) {
	fixed(t)
	for _, c := range []string{
		"echo '====' ; true", `echo "====" ; true`, `echo \==== ; true`,
		"cat <<'EOF'\n====\nEOF\ntrue", "cat <<EOF\n====\nEOF\ntrue", "# ==== \ntrue",
	} {
		if h := Scan(c); len(h) != 0 {
			t.Errorf("Scan(%q) = %v", c, h)
		}
	}
}

func FuzzScan(f *testing.F) {
	for _, p := range probes {
		f.Add(p.line + "; touch M")
	}
	f.Add("a==b; true")
	f.Add("x=(====); y")
	f.Add("[[ a == ==== ]]; y")
	f.Fuzz(func(t *testing.T, s string) {
		old := Lookup
		defer func() { Lookup = old }()
		Lookup = func(string) bool { return false } // no PATH work in a fuzz loop
		for _, h := range Scan(s) {
			if h.Off < 0 || h.Off >= len(s) || !strings.HasPrefix(s[h.Off:], h.Word) {
				t.Fatalf("Hit %+v does not point at its word in %q", h, s)
			}
			if h.Lost == "" {
				t.Fatalf("Hit %+v with nothing lost in %q", h, s)
			}
		}
	})
}
