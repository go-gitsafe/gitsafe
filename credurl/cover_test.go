package credurl

import "testing"

// Strip reads its own result back and gives up, leaving the input untouched,
// whenever the arithmetic and Inspect's parse disagree. No real URL makes them
// disagree, so Inspect is replaced by one that says "secret" for the input and
// "clean" for anything Strip derives from it: then a guard that is missing
// would let a mangled rewrite through as a success.
func TestStripGivesUpWhenItsOwnParseDisagrees(t *testing.T) {
	old := inspectFn
	defer func() { inspectFn = old }()
	for name, in := range map[string]string{
		"no scheme":            "us@host:o/r",
		"no @ in authority":    "https://host/o/r",
		"@ only past the path": "https://host/o/r@x",
	} {
		// The first call sees the leak; every later one reads clean, even of the
		// unchanged input, so only Strip's own parse can refuse.
		calls := 0
		inspectFn = func(s string) Finding {
			calls++
			if calls == 1 {
				return Finding{Verdict: Secret}
			}
			return Finding{Verdict: Username}
		}
		if out, changed := Strip(in); changed || out != in {
			t.Errorf("%s: Strip(%q) = %q, %v", name, in, out, changed)
		}
	}
}

func TestStripGivesUpWhenTheResultStillLeaks(t *testing.T) {
	old := inspectFn
	inspectFn = func(string) Finding { return Finding{Verdict: Secret} }
	defer func() { inspectFn = old }()
	const in = "https://tok@host/o/r"
	if out, changed := Strip(in); changed || out != in {
		t.Errorf("Strip(%q) = %q, %v", in, out, changed)
	}
}

// An unprefixed high-entropy PASSWORD half is a generic secret, named as one, not
// the catch-all "password embedded in a URL".
func TestAGenericPasswordHalfIsNamedAsGeneric(t *testing.T) {
	const pw = "Xk9fQ2mZp7LwR4tYv8NbC3dH6jS1aGuE"
	if !Generic(pw) {
		t.Fatalf("test password is not Generic")
	}
	f := Inspect("https://alice:" + pw + "@host/o/r")
	if !f.Leak() || f.Shape != genericName || f.Half != "password" {
		t.Errorf("Inspect = %+v", f)
	}
}
