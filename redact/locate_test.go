package redact

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// sameAsWholeText fails when the needle-and-run scan finds anything
// different from running the shape regexps over the whole text, as Find did
// before.
func sameAsWholeText(t *testing.T, s string) {
	t.Helper()
	// String as it was: ReplaceAllStringFunc over the whole text.
	old := func(s string) string {
		for _, re := range shapes {
			s = re.ReplaceAllStringFunc(s, func(m string) string {
				if strings.HasPrefix(m, "://") {
					return "://" + Mask + "@"
				}
				return Mask
			})
		}
		return s
	}
	if got, want := New().String(s), old(s); got != want {
		t.Fatalf("String(%q):\nnow %q\nwas %q", s, got, want)
	}
	for i, got := range [][][]int{locateTokens(s), locateURLCredentials(s)} {
		want := shapes[i].FindAllStringIndex(s, -1)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("shape %d on %q:\nruns  %v\nwhole %v", i, s, got, want)
		}
	}
}

// pieces favours the needles and the bytes around them.
var pieces = []string{"ghp_", "github_pat_", "glpat-", "xoxb-", "AKIA", "ASIA", "://", "@", ":", "/", " ", "\n",
	"0", "a", "Z", "_", "-", ".", "aB3", "é", "user", "https", "x", "AAAAAAAAAAAA", "0123456789abcdef"}

func TestLocateMatchesWholeText(t *testing.T) {
	gl := "glpat-" + strings.Repeat("a", 20) + ".01.abc"
	for _, s := range []string{
		benchPrompt(t), "", "://", "https://u:p@h/x and ghp_" + strings.Repeat("A", 36), gl, "x" + gl + "y",
		"ghp_" + strings.Repeat("0", 36) + "ghp_" + strings.Repeat("1", 36), "AKIA" + strings.Repeat("Z", 16) + " ASIA",
		"https://a:b@c https://d:e@f", "://a:b@://c:d@",
	} {
		sameAsWholeText(t, s)
	}
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := r.IntN(30); n > 0; n-- {
			p := pieces[r.IntN(len(pieces))]
			for k := r.IntN(4) + 1; k > 0; k-- {
				b.WriteString(p)
			}
		}
		sameAsWholeText(t, b.String())
	}
}

func FuzzLocateMatchesWholeText(f *testing.F) {
	f.Add("see https://user:pass@example.org and ghp_" + strings.Repeat("A", 36))
	f.Add("glpat-" + strings.Repeat("a", 20) + ".01.abc xoxb-12345678 AKIA" + strings.Repeat("Z", 16))
	f.Fuzz(func(t *testing.T, s string) { sameAsWholeText(t, s) })
}
