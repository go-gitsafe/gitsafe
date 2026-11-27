package relver

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseCore(t *testing.T) {
	cases := []struct {
		in   string
		v    Version
		rest string
		ok   bool
	}{
		{"v1.2.3", Version{1, 2, 3}, "", true},
		{"v0.0.0", Version{}, "", true},
		{"v10.20.30", Version{10, 20, 30}, "", true},
		{"v123456789.0.0", Version{123456789, 0, 0}, "", true},
		{"v1.2.3-rc.1", Version{1, 2, 3}, "-rc.1", true},
		{"v1.2.3+build", Version{1, 2, 3}, "+build", true},
		{"1.2.3", Version{}, "", false},
		{"v1.2", Version{}, "", false},
		{"v01.2.3", Version{}, "", false},
		{"v1.02.3", Version{}, "", false},
		{"v1.2.03", Version{}, "", false},
		{"v1..3", Version{}, "", false},
		{"v1.2.x", Version{}, "", false},
		{"v-1.2.3", Version{}, "", false},
		{"v1234567890.0.0", Version{}, "", false}, // 10 digits: refused, never overflowed
		{"", Version{}, "", false},
	}
	for _, c := range cases {
		v, rest, ok := ParseCore(c.in)
		if ok != c.ok || (ok && (v != c.v || rest != c.rest)) {
			t.Errorf("ParseCore(%q) = %v %q %v, want %v %q %v", c.in, v, rest, ok, c.v, c.rest, c.ok)
		}
	}
}

// Asserted on their own: with the length guard gone a four-part name crashes
// the loop, and a crash is not the assertion.
func TestParseCoreTooFewParts(t *testing.T) {
	for _, s := range []string{"v1", "v1.2", "v"} {
		if v, _, ok := ParseCore(s); ok {
			t.Errorf("ParseCore(%q) = %v, accepted", s, v)
		}
	}
}

// noPanic runs f and turns a panic into a failure of the test that called it,
// so the guard under test is held by an assertion and not by a crash.
func noPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

func TestParseCoreTooManyParts(t *testing.T) {
	noPanic(t, "ParseCore(v1.2.3.4)", func() {
		if v, _, ok := ParseCore("v1.2.3.4"); ok {
			t.Errorf("ParseCore(v1.2.3.4) = %v, accepted", v)
		}
	})
}

// With no slash there is no directory; slicing at the slash's index would panic.
func TestSplitTagWithoutASlash(t *testing.T) {
	noPanic(t, "SplitTag", func() {
		if dir, ver, ok := SplitTag("v1.0.0"); !ok || dir != "" || ver != "v1.0.0" {
			t.Errorf("v1.0.0: %q %q %v", dir, ver, ok)
		}
		if _, _, ok := SplitTag("1.0.0"); ok {
			t.Error("1.0.0 has no v")
		}
		if _, _, ok := SplitTag(""); ok {
			t.Error("the empty tag is not a tag")
		}
	})
}

func TestParseStrict(t *testing.T) {
	if v, err := ParseStrict("v1.2.3"); err != nil || v != (Version{1, 2, 3}) {
		t.Errorf("ParseStrict(v1.2.3) = %v, %v", v, err)
	}
	for _, bad := range []string{"v1.2.3-rc.1", "v1.2.3+b", "v1.2", "1.2.3", "v01.2.3"} {
		if _, err := ParseStrict(bad); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("ParseStrict(%q) = %v, want an error naming it", bad, err)
		}
	}
}

func TestCmpAndNext(t *testing.T) {
	cases := []struct {
		a, b Version
		want int
	}{
		{Version{1, 2, 3}, Version{1, 2, 3}, 0},
		{Version{1, 2, 3}, Version{1, 2, 4}, -1},
		{Version{1, 2, 4}, Version{1, 2, 3}, 1},
		{Version{1, 2, 9}, Version{1, 3, 0}, -1},
		{Version{1, 3, 0}, Version{1, 2, 9}, 1},
		{Version{1, 9, 9}, Version{2, 0, 0}, -1},
		{Version{2, 0, 0}, Version{1, 9, 9}, 1},
		{Version{0, 10, 0}, Version{0, 9, 0}, 1}, // numeric, not lexical
	}
	for _, c := range cases {
		if got := c.a.Cmp(c.b); got != c.want {
			t.Errorf("%v.Cmp(%v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	v := Version{1, 2, 3}
	if v.NextPatch() != (Version{1, 2, 4}) || v.NextMinor() != (Version{1, 3, 0}) || v.NextMajor() != (Version{2, 0, 0}) {
		t.Errorf("next versions of %v: %v %v %v", v, v.NextPatch(), v.NextMinor(), v.NextMajor())
	}
	if v.String() != "v1.2.3" {
		t.Errorf("String = %q", v.String())
	}
}

// GitHub lists tags in no semver order: v0.9.0 after v0.10.0 is the usual case.
func TestHighest(t *testing.T) {
	cases := []struct {
		names []string
		want  Version
		found bool
	}{
		{nil, Version{}, false},
		{[]string{"junk", "v1.0.0-rc.1", "latest"}, Version{}, false},
		{[]string{"v0.10.0", "v0.9.0", "v0.2.0"}, Version{0, 10, 0}, true},
		{[]string{"v0.2.0", "v0.9.0", "v0.10.0"}, Version{0, 10, 0}, true},
		{[]string{"v1.0.0", "v2.0.0-rc.1"}, Version{1, 0, 0}, true}, // a pre-release is not the highest
		{[]string{"v1.2.3+meta", "v1.2.2"}, Version{1, 2, 3}, true},
		{[]string{"v0.0.0"}, Version{}, true},
	}
	for _, c := range cases {
		got, found := Highest(c.names)
		if found != c.found || got != c.want {
			t.Errorf("Highest(%v) = %v %v, want %v %v", c.names, got, found, c.want, c.found)
		}
	}
}

func TestSplitTag(t *testing.T) {
	cases := []struct {
		tag, dir, ver string
		ok            bool
	}{
		{"v1.0.0", "", "v1.0.0", true},
		{"sub/v1.0.0", "sub", "v1.0.0", true},
		{"a/b/v2.0.0", "a/b", "v2.0.0", true},
		{"sub/1.0.0", "", "", false},
		{"/v1.0.0", "", "", false},
		{"a//v1.0.0", "", "", false},
		{"../v1.0.0", "", "", false},
		{"./v1.0.0", "", "", false},
		{"a b/v1.0.0", "", "", false},
		{"a:b/v1.0.0", "", "", false},
	}
	for _, c := range cases {
		dir, ver, ok := SplitTag(c.tag)
		if dir != c.dir || ver != c.ver || ok != c.ok {
			t.Errorf("SplitTag(%q) = %q %q %v, want %q %q %v", c.tag, dir, ver, ok, c.dir, c.ver, c.ok)
		}
	}
}

func TestLine(t *testing.T) {
	tags := []string{"v1.0.0", "v1.1.0", "sub/v0.3.0", "sub/deep/v0.4.0", "other/v9.0.0", "sub/v0.2.0"}
	if got, want := Line(tags, ""), []string{"v1.0.0", "v1.1.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("root line = %v, want %v", got, want)
	}
	if got, want := Line(tags, "sub/"), []string{"v0.3.0", "v0.2.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sub line = %v, want %v", got, want)
	}
	if got := Line(tags, "none/"); len(got) != 0 {
		t.Errorf("none line = %v", got)
	}
}
