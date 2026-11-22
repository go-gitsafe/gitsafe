package issuegate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

const self = "go-gitsafe/gitsafe"

// ⛔⛔ THE KIND MUST BE INSIDE THE COMPARISON. This table compared
// `fmt.Sprint(got) != fmt.Sprint(want)`, and Ref.String() prints only
// `owner/repo#N` -- the Kind is not in it. So every Closes/Refs/Bare expectation
// in this file was unasserted: the rows that exist ONLY to pin a Kind ("closes
// only the next one", "duplicate: closing wins", "duplicate: refs over bare",
// the nine keywords) passed whatever Parse decided, and this is the gate that
// answers "does merging this close the issue?".
//
// Found on 2026-11-22 while break-checking a different fix (#165): a deliberately
// broken Strip made `Closes ...span... #5` come back as Closes, and the row
// asserting Bare PASSED. A witness that cannot fail is not a witness.
func withKind(rs []Ref) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s(%s)", r, r.Kind))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func TestParse(t *testing.T) {
	cases := []struct {
		name, body string
		want       []Ref
	}{
		{"closes", "Closes #1", []Ref{{self, 1, Closes}}},
		{"every keyword", "close #1 closes #2 closed #3 fix #4 fixes #5 fixed #6 resolve #7 resolves #8 resolved #9",
			[]Ref{{self, 1, Closes}, {self, 2, Closes}, {self, 3, Closes}, {self, 4, Closes}, {self, 5, Closes},
				{self, 6, Closes}, {self, 7, Closes}, {self, 8, Closes}, {self, 9, Closes}}},
		{"upper case and colon", "FIXES: #4", []Ref{{self, 4, Closes}}},
		{"refs", "Refs #2", []Ref{{self, 2, Refs}}},
		{"refs case-insensitive", "refs #2, REFS #3", []Ref{{self, 2, Refs}, {self, 3, Refs}}},
		{"bare", "see #3 for context", []Ref{{self, 3, Bare}}},
		{"owner/repo", "Fixes go-fleettools/fleettools#12", []Ref{{"go-fleettools/fleettools", 12, Closes}}},
		{"owner/repo bare", "context: other/repo#7", []Ref{{"other/repo", 7, Bare}}},
		{"closes only the next one", "Closes #1, #2", []Ref{{self, 1, Closes}, {self, 2, Bare}}},
		{"keyword each", "Resolves #10, resolves #123", []Ref{{self, 10, Closes}, {self, 123, Closes}}},
		{"refs list", "Refs #1, #2 and #3 & #4", []Ref{{self, 1, Refs}, {self, 2, Refs}, {self, 3, Refs}, {self, 4, Refs}}},
		{"keyword on another line does not count", "Closes\n#5", []Ref{{self, 5, Bare}}},
		{"keyword is a whole word", "suffixes #5 and prefixed #6", []Ref{{self, 5, Bare}, {self, 6, Bare}}},
		{"fenced block", "text\n```\nCloses #9\n#10\n```\nafter #11", []Ref{{self, 11, Bare}}},
		{"unterminated fence", "a #1\n```\n#2", []Ref{{self, 1, Bare}}},
		{"tilde fence", "~~~\n#2\n~~~", nil},
		{"code span", "use `#8` or `Closes #9` here", nil},
		// ⛔ A CODE SPAN MAY WRAP A LINE BREAK (CommonMark), and reflowing a
		// paragraph to 80 columns moves line breaks into spans invisibly -- the
		// diff of a rewrapped paragraph does not show it. The pattern this
		// replaced, "`[^`\n]*`", refused that newline, so both halves of a
		// wrapped span were read as prose (#165).
		{"code span wrapping a line break", "use `#8 and\n#9` here", nil},
		// ⛔⛔ THE HALF WITH TEETH. A merely QUOTED closing keyword counted as a
		// real decision, and GitHub's own parser excludes the span and closes
		// nothing: the gate would have allowed a merge on a disposition the
		// forge never carries out, leaving the issue open with a merged pull
		// request pointing at it -- the state openfixed then reports as needing
		// a human.
		{"a quoted closing keyword inside a wrapped span decides nothing",
			"see `a span, Closes #204\nand it keeps going` here", nil},
		// ⛔⛔ AND THE LINE BREAKS A SPAN COVERED MUST SURVIVE IT. Parse splits
		// on them and a keyword binds only within its own line, so collapsing a
		// wrapped span to one space would let this `Closes` reach across to #5
		// -- which is go-fleettools/fleettools#205 exactly (a heading bound to a
		// reference two lines below). Bare is the whole point of this row.
		{"a wrapped span keeps the lines it covered apart", "Closes `x\ny` #5", []Ref{{self, 5, Bare}}},
		// An unclosed run is literal text, and a later run may still open a span.
		{"an unclosed backtick is text, not a span", "a ` and #5 here", []Ref{{self, 5, Bare}}},
		{"a run closes only on a run of its own length", "``a `b` c`` then #7", []Ref{{self, 7, Bare}}},
		// A run pairs with the NEXT run of its own length, not the last one: the
		// three backticks here make a span of " #4 and " and leave the third
		// literal, so #4 is code and #5 is prose. Written expecting the
		// opposite, and corrected by this row going red -- "unclosed" was a
		// guess about a body that has an even pair in it.
		{"a run pairs with the next run of its length, not the last", "` #4 and `#5` after", []Ref{{self, 5, Bare}}},
		{"html comment", "<!-- Closes #1 -->\nbody", nil},
		{"pull request url", "see https://github.com/o/r/pull/12#issuecomment-5 and http://x.y/#3", nil},
		{"issue url after keyword", "Fixes https://github.com/o/r/issues/4", []Ref{{"o/r", 4, Closes}}},
		{"in parentheses", "(Closes #6)", []Ref{{self, 6, Closes}}},
		{"duplicate: closing wins", "see #3\nCloses #3\nRefs #3", []Ref{{self, 3, Closes}}},
		{"duplicate: refs over bare", "see #3\nRefs #3", []Ref{{self, 3, Refs}}},
		{"not a reference", "C#5 a&#8217; x#y foo/bar/baz#4 ##6 #", nil},
		{"huge number rejected by width", "#1234567890", nil},
		{"zero", "Closes #0", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Parse(c.body, self)
			if withKind(got) != withKind(c.want) {
				t.Errorf("Parse(%q)\n got %s\nwant %s", c.body, withKind(got), withKind(c.want))
			}
		})
	}
}

// fake is GitHub, as the gate sees it.
type fake struct {
	issues      map[string]Issue
	failGet     map[string]error
	failShut    map[string]error
	autoShut    map[string]bool // GitHub closes it by itself once merged
	merged      bool
	failComment bool
	gets        []string
	comments    []string
	shut        []string
}

func key(repo string, n int) string { return fmt.Sprintf("%s#%d", repo, n) }

func (f *fake) Issue(repo string, n int) (Issue, error) {
	k := key(repo, n)
	f.gets = append(f.gets, k)
	if err := f.failGet[k]; err != nil {
		return Issue{}, err
	}
	is := f.issues[k] // zero: does not exist
	if f.merged && f.autoShut[k] {
		is.Open = false
	}
	return is, nil
}
func (f *fake) Comment(repo string, n int, text string) error {
	if f.failComment {
		return errors.New("comment refused")
	}
	f.comments = append(f.comments, key(repo, n)+": "+text)
	return nil
}
func (f *fake) Close(repo string, n int) error {
	k := key(repo, n)
	if err := f.failShut[k]; err != nil {
		return err
	}
	f.shut = append(f.shut, k)
	return nil
}

func open() Issue { return Issue{Exists: true, Open: true} }
func shut() Issue { return Issue{Exists: true} }
func pull() Issue { return Issue{Exists: true, IsPR: true, Open: true} }
func newFake() *fake {
	return &fake{issues: map[string]Issue{}, failGet: map[string]error{}, failShut: map[string]error{}, autoShut: map[string]bool{}}
}

func TestClassifySortsByWhatGitHubSays(t *testing.T) {
	f := newFake()
	f.issues[key(self, 1)] = open() // closes
	f.issues[key(self, 2)] = open() // refs
	f.issues[key(self, 3)] = open() // bare
	f.issues[key(self, 4)] = pull() // a pull request: ignored
	f.issues[key(self, 5)] = shut() // closed already: nothing to leave open
	// 6 does not exist at all
	body := "Closes #1\nRefs #2\nsee #3, #4, #5 and #6" // only the first of a list carries a keyword
	body = "Closes #1\nRefs #2\nsee #3\nsee #4\nsee #5\nsee #6"
	v, err := Classify(f, body, self)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Closing) != 1 || len(v.Refs) != 1 || len(v.Unclassed) != 1 || len(v.Ignored) != 3 {
		t.Fatalf("%+v", v)
	}
	if len(f.gets) != 6 {
		t.Errorf("want ONE get per reference (6), made %d: %v", len(f.gets), f.gets)
	}
	why := v.Refusal()
	for _, want := range []string{"go-gitsafe/gitsafe#3", "Closes go-gitsafe/gitsafe#3", "Refs go-gitsafe/gitsafe#3", "--issues-waiver"} {
		if !strings.Contains(why, want) {
			t.Errorf("refusal lacks %q: %s", want, why)
		}
	}
}

func TestNoUnclassifiedMeansNoRefusal(t *testing.T) {
	f := newFake()
	f.issues[key(self, 1)] = open()
	v, err := Classify(f, "Refs #1", self)
	if err != nil || v.Refusal() != "" {
		t.Fatalf("%v %q", err, v.Refusal())
	}
	if v, _ := Classify(f, "", self); v.Refusal() != "" || len(f.gets) != 1 {
		t.Errorf("an empty body must cost no request: %v", f.gets)
	}
}

func TestClassifyFailsClosed(t *testing.T) {
	f := newFake()
	f.failGet[key(self, 1)] = errors.New("boom")
	if _, err := Classify(f, "Closes #1", self); err == nil || !strings.Contains(err.Error(), "go-gitsafe/gitsafe#1") {
		t.Errorf("a failed read must refuse and name the issue: %v", err)
	}
	var b strings.Builder
	for i := 1; i <= MaxRefs+1; i++ {
		fmt.Fprintf(&b, "Refs #%d\n", i)
	}
	f = newFake()
	if _, err := Classify(f, b.String(), self); err == nil || len(f.gets) != 0 {
		t.Errorf("a body over the cap must refuse before spending a request: %v, %d gets", err, len(f.gets))
	}
}

func TestAfterMerge(t *testing.T) {
	f := newFake()
	f.issues[key(self, 1)] = open() // GitHub closes it
	f.issues[key(self, 2)] = open() // GitHub does not (cross-repo, say)
	f.issues[key("o/r", 3)] = open()
	f.issues[key("o/r", 4)] = open() // cannot be closed
	f.issues[key("o/r", 5)] = open() // cannot be re-read
	f.autoShut[key(self, 1)] = true
	f.failShut[key("o/r", 4)] = errors.New("403")
	v, err := Classify(f, "Closes #1\nCloses #2\nFixes o/r#3\nFixes o/r#4\nFixes o/r#5", self)
	if err != nil {
		t.Fatal(err)
	}
	f.merged = true
	f.failGet[key("o/r", 5)] = errors.New("502")
	f.gets = nil
	res := AfterMerge(f, v, self, 42, "0123456789abcdef")
	if len(f.gets) != 5 {
		t.Errorf("want one re-read per closing issue: %v", f.gets)
	}
	if res[0].ByUs || res[0].Error != nil {
		t.Errorf("GitHub closed #1: %+v", res[0])
	}
	if !res[1].ByUs || res[2].Error != nil || !res[2].ByUs {
		t.Errorf("we close what GitHub left open: %+v", res)
	}
	if res[3].Error == nil || res[4].Error == nil {
		t.Errorf("a failure must be reported: %+v", res)
	}
	want := "go-gitsafe/gitsafe#2: Closed by go-gitsafe/gitsafe#42 (merge 0123456789ab)"
	if len(f.comments) < 1 || f.comments[0] != want {
		t.Errorf("comment %q, want %q", f.comments, want)
	}
	if fmt.Sprint(f.shut) != "[go-gitsafe/gitsafe#2 o/r#3]" {
		t.Errorf("closed %v", f.shut)
	}
}

func TestAfterMergeCommentRefused(t *testing.T) {
	f := newFake()
	f.issues[key(self, 1)] = open()
	v, _ := Classify(f, "Closes #1", self)
	f.failComment = true
	res := AfterMerge(f, v, self, 1, "abc")
	if res[0].Error == nil || len(f.shut) != 0 {
		t.Errorf("an issue whose comment failed must be reported, not closed in silence: %+v", res)
	}
}

func TestKindString(t *testing.T) {
	if Closes.String() != "closes" || Refs.String() != "refs" || Bare.String() != "bare" {
		t.Error("names")
	}
	if WaiverStamp("why") != "issues gate waived: why" {
		t.Error("stamp")
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"Closes #1", "Refs #2, #3 and #4", "```\n#1\n```", "`#1`", "<!-- #1", "Fixes o/r#9",
		"https://github.com/o/r/issues/4", "\x00#1", "Closes ", "#", "~~~", "a/b#", "Fixes: o/r#1, resolves #2 & #3",
		// Code-span shapes, which decide whether a reference is prose (#165).
		"`#1 and\n#2`", "Closes `x\ny` #5", "``#1 `#2` #3``", "` #1 and `#2` after",
		"`", "``", "```", "`#1", "#1`", "`\n`#2`\n`",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		refs := Parse(body, self)
		seen := map[string]bool{}
		for _, r := range refs {
			if r.Number <= 0 || r.Repo == "" || !strings.Contains(r.Repo, "/") {
				t.Fatalf("malformed %+v from %q", r, body)
			}
			if r.Kind < Bare || r.Kind > Closes {
				t.Fatalf("kind %d", r.Kind)
			}
			k := strings.ToLower(r.String())
			if seen[k] {
				t.Fatalf("duplicate %s from %q", k, body)
			}
			seen[k] = true
			// Leading zeros are allowed: "#01" is issue 1.
			if !regexp.MustCompile(fmt.Sprintf(`#0*%d|/issues/0*%d`, r.Number, r.Number)).MatchString(body) {
				t.Fatalf("%s is in no way in %q", r, body)
			}
		}
		// ⛔ The KIND is in this comparison, for the reason given above withKind:
		// `fmt.Sprint` of a []Ref does not print it, so determinism was asserted
		// about the numbers only -- a Parse that answered Closes and then Refs
		// for the same body would have passed.
		if again := Parse(body, self); withKind(again) != withKind(refs) {
			t.Fatalf("not deterministic: %s then %s", withKind(refs), withKind(again))
		}
	})
}

// FuzzStripCodeSpansKeepsEveryLineBreak is the invariant the wrapped-span fix
// turns on, and the one whose absence would be a silent regression of
// go-fleettools/fleettools#205.
//
// Parse splits Strip's output on newlines and a closing keyword binds only
// within its own line. So removing a span that WRAPPED a line break must leave
// that break behind: collapse it and a `Closes` at the end of one line reaches a
// reference on the next, which is a merge gate closing an issue nobody said to
// close. Line endings are therefore conserved exactly -- none added either,
// since an invented break would split a keyword from the reference it owns.
//
// It also says the scanner terminates and never panics on arbitrary runs of
// backticks, which is the part a pattern used to guarantee for free.
func FuzzStripCodeSpansKeepsEveryLineBreak(f *testing.F) {
	for _, s := range []string{
		"`#1 and\n#2`", "Closes `x\ny` #5", "``a `b` c``", "`", "``", "```\n```",
		"`\n`\n`", "no backticks at all", "", "`a`b`c`", "\n\n`\n\n", "`\r\n`",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		out := stripCodeSpans(body)
		if got, want := strings.Count(out, "\n"), strings.Count(body, "\n"); got != want {
			t.Fatalf("%d line break(s) out of %d in:\n%q\ngave:\n%q", got, want, body, out)
		}
		if again := stripCodeSpans(out); strings.Count(again, "\n") != strings.Count(out, "\n") {
			t.Fatalf("a second pass moved the line breaks of %q", out)
		}
	})
}
