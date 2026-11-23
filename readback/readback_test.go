package readback

import (
	"strings"
	"testing"
)

// Occurrence 4, copied from the session that measured it: priorart exited 2 and
// the line reported cat's 0.
const founding = `priorart --repo o/r "x" > f 2>&1; brief -n 25 -- cat f`

func TestTheFoundingCaseIsRefusedAndNamesBothStatements(t *testing.T) {
	f := Check(founding)
	if !f.Found() {
		t.Fatal("the founding case is allowed")
	}
	if f.Writer != `priorart --repo o/r "x" > f 2>&1` || f.Reader != "brief -n 25 -- cat f" ||
		f.Target != "f" || f.Sep != ";" {
		t.Errorf("finding = %+v", f)
	}
	for _, s := range []string{f.String(), f.Why(), Advice} {
		if s == "" {
			t.Error("a refusal with nothing in it")
		}
	}
	if !strings.Contains(Advice, "brief -n") || !strings.Contains(Advice, "NEXT turn") {
		t.Error("the advice does not carry the two remedies")
	}
	var none Finding
	if none.Found() || none.String() != "" {
		t.Error("the zero value reads as a finding")
	}
}

func TestTheStringSpellsANewlineAndASeparator(t *testing.T) {
	if s := Check("cmd > f\ncat f").String(); !strings.Contains(s, "a newline joins") {
		t.Errorf("a newline is not spelled as one: %s", s)
	}
	if s := Check("cmd > f; cat f").String(); !strings.Contains(s, "`;` joins") {
		t.Errorf("the separator is not named: %s", s)
	}
}

func TestRefusals(t *testing.T) {
	for name, c := range map[string]struct{ cmd, sep string }{
		"or":                {`cmd > f 2>&1 || cat f`, "||"},
		"newline":           {"cmd > f 2>&1\ncat f", "\n"},
		"append":            {`cmd >> f; cat f`, ";"},
		"both streams":      {`cmd &> f; cat f`, ";"},
		"attached target":   {`cmd >f; cat f`, ";"},
		"fd attached":       {`cmd 2>f; cat f`, ";"},
		"quoted target":     {`cmd > "f"; cat 'f'`, ";"},
		"input redirect":    {`cmd > f; wc -l <f`, ";"},
		"a variable":        {`cmd > $L 2>&1; grep -c ok $L`, ";"},
		"after a && link":   {`cmd > f && b; cat f`, ";"},
		"reader behind &&":  {`cmd > f; b && cat f`, ";"},
		"third statement":   {`cmd > f; b; cat f`, ";"},
		"escaped quote":     {`cmd > "a\"b"; cat 'a"b'`, ";"},
		"heredoc-ish input": {`cmd > f; cat <<< f`, ";"},
	} {
		f := Check(c.cmd)
		if !f.Found() {
			t.Errorf("%s: allowed\n  %s", name, c.cmd)
			continue
		}
		if f.Sep != c.sep {
			t.Errorf("%s: Sep = %q, want %q", name, f.Sep, c.sep)
		}
	}
}

func TestAllowed(t *testing.T) {
	for name, cmd := range map[string]string{
		"nothing read back":          `cmd > f 2>&1`,
		"&& keeps the failure":       `cmd > f 2>&1 && brief -n 20 -- cat f`,
		"&& chain":                   `a > f && b && cat f`,
		"later statement writes too": `a > f 2>&1; b >> f`,
		"later writes attached":      `a > f; echo done >f`,
		"dev null":                   `cmd > /dev/null; cat /dev/null`,
		"dev stderr":                 `cmd > /dev/stderr; cat /dev/stderr`,
		"descriptor dup":             `cmd 2>&1; cat 1`,
		"dup then file":              `cmd 2>&1 >&2; cat 2`,
		"another file":               `cmd > f; cat g`,
		"no redirect":                `cd /d; git status`,
		"a mention":                  `echo 'write about cmd > f; cat f here'`,
		"a double-quoted mention":    `echo "cmd > f; cat f"`,
		"a quoted heredoc":           "cat <<'EOF' > msg\ncmd > f; cat f\nEOF",
		"a prefix of the name":       `cmd > f; cat file`,
		"an empty line":              ``,
		"an empty target":            `cmd > ""; cat ""`,
		"a trailing separator":       `cmd > f;`,
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("%s: refused\n  %s\n  %s", name, cmd, f)
		}
	}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		st     string
		words  []string
		writes []string
	}{
		{`a b`, []string{"a", "b"}, nil},
		{`a > f 2>&1`, []string{"a"}, []string{"f"}},
		{`a>>f`, []string{"a"}, []string{"f"}},
		{`a 2> f`, []string{"a"}, []string{"f"}},
		{`a &>f`, []string{"a"}, []string{"f"}},
		{`a 12>f`, []string{"a"}, []string{"f"}},
		{`a b2>f`, []string{"a", "b2"}, []string{"f"}},
		{`a > /dev/null`, []string{"a"}, nil},
		{`a >&2`, []string{"a"}, nil},
		{`a 2>&3`, []string{"a"}, nil},
		{`a >&-`, []string{"a"}, nil},
		{`a >&f`, []string{"a"}, []string{"f"}},
		{`a 2>>f`, []string{"a"}, []string{"f"}},
		{`a &>>f`, []string{"a"}, []string{"f"}},
		{`a < in`, []string{"a", "in"}, nil},
		{`a <<< in`, []string{"a", "in"}, nil},
		{`a '' b`, []string{"a", "", "b"}, nil},
		{`a "x y" 'z'`, []string{"a", "x y", "z"}, nil},
		{`a "x\"y"`, []string{"a", `x"y`}, nil},
		{`a > "x y"`, []string{"a"}, []string{"x y"}},
		{"a\tb", []string{"a", "b"}, nil},
		{`a >	f`, []string{"a"}, []string{"f"}},
	} {
		w, wr := parse(c.st)
		if strings.Join(w, "|") != strings.Join(c.words, "|") || len(w) != len(c.words) {
			t.Errorf("parse(%q) words = %q, want %q", c.st, w, c.words)
		}
		if strings.Join(wr, "|") != strings.Join(c.writes, "|") || len(wr) != len(c.writes) {
			t.Errorf("parse(%q) writes = %q, want %q", c.st, wr, c.writes)
		}
	}
}

func TestIsFD(t *testing.T) {
	for s, want := range map[string]bool{"": false, "2": true, "12": true, "a": false, "2a": false, "a2": false} {
		if isFD(s) != want {
			t.Errorf("isFD(%q) != %v", s, want)
		}
	}
}
