package waiveenv

import "testing"

// Each of these hides a waiver behind a spelling the plain assignment form does
// not cover, and each must still be refused.
func TestWaiversBehindUnusualSpellings(t *testing.T) {
	for _, c := range []string{
		`/usr/bin/env X_SKIP=1 true`,
		`env "A"=1 "X_SKIP"=1 true`,
		`echo "a\"b" ; env "Z_WAIVE"=1 go test`,
	} {
		if f := Check(c); !f.Found() {
			t.Errorf("Check(%q) found nothing", c)
		}
	}
}

// A quoted assignment that is not a waiver, after env, is the program's own
// argument list and is allowed.
func TestAQuotedAssignmentThatIsNotAWaiverIsAllowed(t *testing.T) {
	for _, c := range []string{`env`, `sudo -n`, `env "A"=1 true`, `a\ b=1 true`, `echo "a\\b"`} {
		if f := Check(c); f.Found() {
			t.Errorf("Check(%q) = %v", c, f)
		}
	}
}
