package protect

import (
	"strings"
	"testing"
)

func TestATagIsNotABranch(t *testing.T) {
	// The rule this exists to keep: a release tag goes to a repository whose
	// default branch is protected, all day long. A guard that refused those
	// would be switched off within the hour, and then it would be protecting
	// nothing at all.
	ups := Parse(strings.NewReader(
		"refs/tags/v1.2.3 aaaa refs/tags/v1.2.3 0000000000000000000000000000000000000000\n"))
	if got := Blocked(ups, "main"); len(got) != 0 {
		t.Errorf("a tag was blocked: %v", got)
	}
}

func TestWritingTheDefaultBranchIsBlocked(t *testing.T) {
	ups := Parse(strings.NewReader(
		"refs/heads/main aaaa refs/heads/main bbbb\n"))
	if got := Blocked(ups, "main"); len(got) != 1 || got[0] != "main" {
		t.Errorf("Blocked = %v, want [main]", got)
	}
	// master too, and a repository whose default is neither.
	for branch, def := range map[string]string{"master": "master", "trunk": "trunk"} {
		ups := Parse(strings.NewReader(
			"refs/heads/" + branch + " aaaa refs/heads/" + branch + " bbbb\n"))
		if got := Blocked(ups, def); len(got) != 1 {
			t.Errorf("%s: Blocked = %v, want it blocked", branch, got)
		}
	}
}

func TestAnOrdinaryBranchGoesThrough(t *testing.T) {
	ups := Parse(strings.NewReader(
		"refs/heads/a-fix aaaa refs/heads/a-fix bbbb\n"))
	if got := Blocked(ups, "main"); len(got) != 0 {
		t.Errorf("an ordinary branch was blocked: %v", got)
	}
}

func TestCreatingTheDefaultBranchIsAllowed(t *testing.T) {
	// A repository whose main does not exist has nothing to open a pull
	// request against. That is every first write to a new repository, and
	// refusing it would make this guard the thing that stops work.
	ups := Parse(strings.NewReader(
		"refs/heads/main aaaa refs/heads/main 0000000000000000000000000000000000000000\n"))
	if got := Blocked(ups, "main"); len(got) != 0 {
		t.Errorf("creating main was blocked: %v", got)
	}
}

func TestDeletingTheDefaultBranchIsBlocked(t *testing.T) {
	// Deleting is a write like any other, and a worse one.
	ups := Parse(strings.NewReader(
		"(delete) 0000000000000000000000000000000000000000 refs/heads/main bbbb\n"))
	if got := Blocked(ups, "main"); len(got) != 1 {
		t.Errorf("deleting main was not blocked: %v", got)
	}
}

func TestSeveralRefsAtOnce(t *testing.T) {
	// Everything at once, or a branch and the default branch in one command:
	// the protected one has to be found among the rest.
	ups := Parse(strings.NewReader(
		"refs/heads/a-fix aaaa refs/heads/a-fix bbbb\n" +
			"refs/heads/main cccc refs/heads/main dddd\n" +
			"refs/tags/v1.0.0 eeee refs/tags/v1.0.0 0000000000000000000000000000000000000000\n"))
	got := Blocked(ups, "main")
	if len(got) != 1 || got[0] != "main" {
		t.Errorf("Blocked = %v, want [main]", got)
	}
}

func TestALineThatIsNotFourFieldsIsSkipped(t *testing.T) {
	// A hook that refused because it misread a line would be worse than no
	// hook at all.
	ups := Parse(strings.NewReader("nonsense\n\nrefs/heads/main a b\n"))
	if len(ups) != 0 {
		t.Errorf("parsed %v from lines that are not ref updates", ups)
	}
}

func TestProtectedWithNoRemoteDefault(t *testing.T) {
	// A remote that will not say what its default branch is still gets main
	// and master defended, because those are what it is nearly always called.
	if !Protected("main", "") || !Protected("master", "") {
		t.Error("main and master are not protected without a remote default")
	}
	if Protected("a-fix", "") {
		t.Error("an ordinary branch is protected")
	}
	if Protected("", "main") {
		t.Error("the empty branch name is protected")
	}
}
