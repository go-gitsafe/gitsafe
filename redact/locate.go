package redact

import (
	"regexp"
	"slices"
	"strings"

	"github.com/go-gitsafe/gitsafe/credurl"
)

// Running the two shape regexps over the whole text was the whole cost of
// Find: neither has one literal prefix Go's regexp can search for (the token
// pattern is an alternation of sixteen issuers), so the automaton stepped
// through every byte. Measured on the go-llmflow gateway's 2 KB benchmark
// prompt: about 130 µs, all of it in regexp.
//
// Both patterns match only bytes of a known class and have no assertion, so
// every match lies inside one maximal run of that class, and every match
// contains a known needle: an issuer's prefix for a token, "://" for a URL
// credential. The needles are found with strings.Index (vectorised in the
// standard library) on the whole text, and the regexp runs only on the runs
// that hold one. FindAll on the text and FindAll on those runs, in order,
// return the same positions: TestLocateMatchesWholeText and
// FuzzLocateMatchesWholeText compare them.

// tokenByte is the class of the token pattern: the issuers' prefixes and
// bodies are letters, digits, '_' and '-', and GitLab's routable tail adds
// '.'.
var tokenByte = func() (t [256]bool) {
	for c := 0; c < 256; c++ {
		b := byte(c)
		t[c] = b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '.'
	}
	return
}()

// urlByte is the class of the URL-credential pattern: anything but the
// whitespace of RE2's \s ([\t\n\f\r ]). '/' and '@' appear in a match
// ("://", "@") so they stay in.
var urlByte = func() (t [256]bool) {
	for c := 0; c < 256; c++ {
		t[c] = !strings.ContainsRune("\t\n\f\r ", rune(c))
	}
	return
}()

var tokenPrefixes = func() []string {
	var out []string
	for _, s := range credurl.Shapes() {
		out = append(out, s.Prefix)
	}
	return out
}()

// locate is replaced by the whole-text scan in tests.
var (
	locateTokens = func(s string) [][]int {
		return locateRuns(shapes[0], &tokenByte, tokenPrefixes, s)
	}
	locateURLCredentials = func(s string) [][]int {
		return locateRuns(shapes[1], &urlByte, []string{"://"}, s)
	}
)

// locateRuns returns re's matches in s, running re only on the maximal runs
// of class bytes that contain one of the needles.
func locateRuns(re *regexp.Regexp, class *[256]bool, needles []string, s string) [][]int {
	var runs [][2]int
	for _, n := range needles {
		for i := 0; ; {
			j := strings.Index(s[i:], n)
			if j < 0 {
				break
			}
			p := i + j
			a, b := p, p
			for a > 0 && class[s[a-1]] {
				a--
			}
			for b < len(s) && class[s[b]] {
				b++
			}
			if b > a {
				runs = append(runs, [2]int{a, b})
			}
			i = max(b, p+1)
		}
	}
	if len(runs) == 0 {
		return nil
	}
	// Distinct runs in text order (two needles can fall in one run).
	slices.SortFunc(runs, func(x, y [2]int) int { return x[0] - y[0] })
	var out [][]int
	last := -1
	for _, r := range runs {
		if r[0] == last {
			continue
		}
		last = r[0]
		for _, loc := range re.FindAllStringIndex(s[r[0]:r[1]], -1) {
			out = append(out, []int{loc[0] + r[0], loc[1] + r[0]})
		}
	}
	return out
}
