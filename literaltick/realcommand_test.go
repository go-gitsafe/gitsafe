package literaltick_test

import (
	"os"
	"strings"
	"testing"

	"github.com/go-gitsafe/gitsafe/literaltick"
	"github.com/go-gitsafe/gitsafe/shellcmd"
)

// TestTheRealPRBodyIsAllowed is the command this machine really ran, byte for
// byte, taken from the top of the guardscan table that measured this rule at
// 5 050 refusals. It is a `gh pr create --body "$(cat <<'EOF' … )"` whose prose
// contains "(unlike the default `linear`)".
//
// ⛔ IT IS HERE AS BYTES, NOT AS A RECONSTRUCTION. Six hand-written commands of
// this shape were written first and every one of them was allowed, which read
// as "the shape is fine, the examples must be something else". They were fine:
// none of them happened to put a ) in the prose. Only the real line, bisected
// byte by byte, named the ) as the cause.
func TestTheRealPRBodyIsAllowed(t *testing.T) {
	b, err := os.ReadFile("testdata/prbody.txt")
	if err != nil {
		t.Fatal(err)
	}
	cmd := string(b)

	// The witness first: the fixture must still hold the two things that make
	// it this test's subject, or it proves nothing when someone tidies it.
	if !strings.Contains(cmd, "<<'EOF'") || !strings.Contains(cmd, "`") {
		t.Fatal("the fixture lost its quoted heredoc or its backticks")
	}
	if i := strings.Index(cmd, "<<'EOF'"); !strings.Contains(cmd[i:], ")") {
		t.Fatal("the fixture lost the ) in its prose, which is the whole case")
	}

	if f := literaltick.Check(cmd); f.Found() {
		cls := shellcmd.Classify(cmd)
		t.Errorf("refused a quoted heredoc body at off=%d line=%d class=%q\n  matched: %s",
			f.Off, strings.Count(cmd[:f.Off], "\n")+1, cls[f.Off], f.Context)
	}

	// And the classification itself, so a later change that allows this
	// command for some other reason does not pass for the right answer.
	cls := shellcmd.Classify(cmd)
	at := strings.Index(cmd, "`strategy: free`")
	if got := cls[at]; got != shellcmd.ClassHeredocQuoted {
		t.Errorf("the body byte at %d classifies as %q, want %q (a quoted heredoc body)",
			at, got, shellcmd.ClassHeredocQuoted)
	}
}
