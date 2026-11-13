package issuegate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

const self = "go-gitsafe/gitsafe"

func TestParse(t *testing.T) {
	cases := []struct {
		name, body string
		want       []Ref
	}{
		{"closes", "Closes #1", []Ref{{self, 1, Closes, false}}},
		{"every keyword", "close #1 closes #2 closed #3 fix #4 fixes #5 fixed #6 resolve #7 resolves #8 resolved #9",
			[]Ref{{self, 1, Closes, false}, {self, 2, Closes, false}, {self, 3, Closes, false}, {self, 4, Closes, false}, {self, 5, Closes, false},
				{self, 6, Closes, false}, {self, 7, Closes, false}, {self, 8, Closes, false}, {self, 9, Closes, false}}},
		{"upper case and colon", "FIXES: #4", []Ref{{self, 4, Closes, false}}},
		{"refs", "Refs #2", []Ref{{self, 2, Refs, false}}},
		{"refs case-insensitive", "refs #2, REFS #3", []Ref{{self, 2, Refs, false}, {self, 3, Refs, false}}},
		{"bare", "see #3 for context", []Ref{{self, 3, Bare, false}}},
		{"owner/repo", "Fixes go-fleettools/fleettools#12", []Ref{{"go-fleettools/fleettools", 12, Closes, false}}},
		{"owner/repo bare", "context: other/repo#7", []Ref{{"other/repo", 7, Bare, false}}},
		{"closes only the next one", "Closes #1, #2", []Ref{{self, 1, Closes, false}, {self, 2, Bare, false}}},
		{"keyword each", "Resolves #10, resolves #123", []Ref{{self, 10, Closes, false}, {self, 123, Closes, false}}},
		{"refs list", "Refs #1, #2 and #3 & #4", []Ref{{self, 1, Refs, false}, {self, 2, Refs, false}, {self, 3, Refs, false}, {self, 4, Refs, false}}},
		{"keyword on another line does not count", "Closes\n#5", []Ref{{self, 5, Bare, false}}},
		{"keyword is a whole word", "suffixes #5 and prefixed #6", []Ref{{self, 5, Bare, false}, {self, 6, Bare, false}}},
		{"fenced block", "text\n```\nCloses #9\n#10\n```\nafter #11", []Ref{{self, 11, Bare, false}}},
		{"unterminated fence", "a #1\n```\n#2", []Ref{{self, 1, Bare, false}}},
		{"tilde fence", "~~~\n#2\n~~~", nil},
		{"code span", "use `#8` or `Closes #9` here", nil},
		{"html comment", "<!-- Closes #1 -->\nbody", nil},
		{"pull request url", "see https://github.com/o/r/pull/12#issuecomment-5 and http://x.y/#3", nil},
		{"issue url after keyword", "Fixes https://github.com/o/r/issues/4", []Ref{{"o/r", 4, Closes, false}}},
		{"in parentheses", "(Closes #6)", []Ref{{self, 6, Closes, false}}},
		{"duplicate: closing wins", "see #3\nCloses #3\nRefs #3", []Ref{{self, 3, Closes, false}}},
		{"duplicate: refs over bare", "see #3\nRefs #3", []Ref{{self, 3, Refs, false}}},
		{"not a reference", "C#5 a&#8217; x#y foo/bar/baz#4 ##6 #", nil},
		{"huge number rejected by width", "#1234567890", nil},
		{"zero", "Closes #0", nil},

		// ⛔ A NEGATION CHANGES NOTHING FOR GITHUB, so Kind stays Closes and the
		// contradiction is flagged. The sentence that provoked this closed a
		// real issue on merge.
		{"the sentence that closed an issue", "It does not fix #17", []Ref{{self, 17, Closes, true}}},
		{"contraction", "This doesn't close #4", []Ref{{self, 4, Closes, true}}},
		{"a word between", "It does not actually fix #17", []Ref{{self, 17, Closes, true}}},
		// ⛔ Written as a negation case and measured as the opposite: GitHub's
		// list has no gerunds (close/closes/closed, never "closing"), so this
		// sentence carries no keyword to contradict. Kept, because it is the
		// phrasing one reaches for when trying to be careful.
		{"a gerund is no keyword, so there is nothing to contradict", "Landing this without closing #9",
			[]Ref{{self, 9, Bare, false}}},
		{"without, with a keyword GitHub reads", "Shipped without fixes #9", []Ref{{self, 9, Closes, true}}},
		{"never", "This never resolves #3", []Ref{{self, 3, Closes, true}}},
		// ⛔ The cases the rule must NOT catch, each a sentence somebody writes.
		{"refs under a negation is not a contradiction", "This does not finish it; Refs #7",
			[]Ref{{self, 7, Refs, false}}},
		{"a negation far earlier is not this sentence", "It does not matter which branch you take when you close #8",
			[]Ref{{self, 8, Closes, false}}},
		{"a plain close is untouched", "Closes #2", []Ref{{self, 2, Closes, false}}},
		{"negation in a code span is stripped first", "`does not` fix #5", []Ref{{self, 5, Closes, false}}},
		{"one contradicting sentence is enough", "Closes #6\nand it does not close #6",
			[]Ref{{self, 6, Closes, true}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Parse(c.body, self)
			if show(got) != show(c.want) {
				t.Errorf("Parse(%q)\n got %s\nwant %s", c.body, show(got), show(c.want))
			}
		})
	}
}

// ⛔⛔ THIS TABLE WAS VACUOUS ABOUT THE THING IT EXISTS TO CHECK. It compared
// fmt.Sprint of the two slices — and Ref has a String method that prints
// "owner/repo#N" and nothing else, so every Kind in the want column above was
// decoration: "Closes #1" and "see #1" compared EQUAL. Found by mutation, not
// by reading: removing the negation check left the suite green, and removing
// the keyword classification would have too.
//
// show prints what the case is actually about.
func show(refs []Ref) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = fmt.Sprintf("%s/%s/negated=%v", r, r.Kind, r.Negated)
	}
	return "[" + strings.Join(parts, " ") + "]"
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
		if again := Parse(body, self); fmt.Sprint(again) != fmt.Sprint(refs) {
			t.Fatalf("not deterministic")
		}
	})
}
