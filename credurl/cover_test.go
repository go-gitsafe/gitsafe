package credurl

import "testing"

// Strip reads its own result back and gives up, leaving the input untouched,
// whenever the arithmetic and Inspect's parse disagree. No real URL makes them
// disagree, so Inspect is replaced by one that always says "secret".
func TestStripGivesUpWhenItsOwnParseDisagrees(t *testing.T) {
	old := inspectFn
	inspectFn = func(string) Finding { return Finding{Verdict: Secret} }
	defer func() { inspectFn = old }()
	for name, in := range map[string]string{
		"no scheme":            "ghp_x@host:o/r",
		"no @ in authority":    "https://host/o/r",
		"still leaks after":    "https://tok@host/o/r",
		"@ only past the path": "https://host/o/r@x",
	} {
		if out, changed := Strip(in); changed || out != in {
			t.Errorf("%s: Strip(%q) = %q, %v", name, in, out, changed)
		}
	}
}
