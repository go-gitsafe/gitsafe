package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bodyFile(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func prbody(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	rc := run(args, &out, &errb)
	return rc, out.String(), errb.String()
}

// ⛔⛔ THE MECHANISM AS MEASURED, NOT AS I FIRST WROTE IT DOWN.
//
// The body that refused a merge on 2026-11-09 said
// `Refs go-zpkg/pantry-overlay#89, #88`, and I recorded the cause as the comma
// — "a keyword governs the reference that FOLLOWS it". Running this command
// against that body said otherwise: the comma carries the keyword, and what it
// does not carry is the REPOSITORY. A bare `#88` is this repository's #88.
//
// So the test asserts the repository resolution, which is the real trap, and
// would have failed against the explanation I believed.
func TestABareNumberMeansThisRepository(t *testing.T) {
	p := bodyFile(t, "Body.\n\nRefs go-zpkg/pantry-overlay#89, #88\n")
	rc, out, _ := prbody(t, p, "--repo", "go-zpkg/zbk")
	if rc != 0 {
		t.Fatalf("rc = %d, want 0: both references carry a keyword", rc)
	}
	for _, want := range []string{
		"refs    go-zpkg/pantry-overlay#89",
		// NOT go-zpkg/pantry-overlay#88: the comma kept the keyword and lost the
		// repository.
		"refs    go-zpkg/zbk#88",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "go-zpkg/pantry-overlay#88") {
		t.Errorf("a bare #88 was resolved to the OTHER repository:\n%s", out)
	}
}

// ⛔⛔ THE COMMA DEPENDS ON THE KEYWORD, which is the part I got wrong twice.
// A closing keyword governs the ONE reference after it — GitHub's rule — while
// `Refs` is deliberately generous, because keeping an issue open is the safe
// answer. A test for only one of them would have confirmed either of my two
// wrong general rules.
func TestAClosingKeywordGovernsOneReferenceAndRefsGovernsTheList(t *testing.T) {
	closes := bodyFile(t, "Closes other/repo#1, #2\n")
	rc, out, _ := prbody(t, closes, "--repo", "me/mine")
	if rc != 1 {
		t.Fatalf("rc = %d, want 1: #2 is bare after a closing keyword", rc)
	}
	if !strings.Contains(out, "closes  other/repo#1") || !strings.Contains(out, "bare    me/mine#2") {
		t.Errorf("a closing keyword did not stop at the first reference:\n%s", out)
	}

	refs := bodyFile(t, "Refs other/repo#1, #2 and #3\n")
	rc, out, _ = prbody(t, refs, "--repo", "me/mine")
	if rc != 0 {
		t.Fatalf("rc = %d, want 0: Refs keeps the whole list open", rc)
	}
	for _, want := range []string{"refs    other/repo#1", "refs    me/mine#2", "refs    me/mine#3"} {
		if !strings.Contains(out, want) {
			t.Errorf("Refs did not carry across the list, missing %q:\n%s", want, out)
		}
	}
}

// ⛔ THE EXEMPTION EXISTS, and I wrote in this command's own comment that it did
// not. issuegate.Strip removes code spans, so a keyword in backticks is prose —
// which is how a body may explain the rule without obeying it.
func TestAKeywordInACodeSpanIsNotAReference(t *testing.T) {
	p := bodyFile(t, "A sentence saying `Closes #9` inside a code span.\n")
	rc, out, _ := prbody(t, p, "--repo", "me/mine")
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "mentions no issue") {
		t.Errorf("a code span was parsed as a reference:\n%s", out)
	}
}

// And the other half of that body: a reference spelled in full in the prose,
// with no keyword, is what the gate actually refuses.
func TestAReferenceSpelledInProseIsBare(t *testing.T) {
	p := bodyFile(t, "…kernel.org/linux-headers is one of the thirteen "+
		"(go-zpkg/pantry-overlay#88).\n\nRefs go-zpkg/pantry-overlay#89\n")
	rc, _, errb := prbody(t, p, "--repo", "go-zpkg/zbk")
	if rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	if !strings.Contains(errb, "go-zpkg/pantry-overlay#88") {
		t.Errorf("stderr does not name the bare reference: %q", errb)
	}
	if !strings.Contains(errb, "ghmerge will refuse") {
		t.Errorf("stderr does not say what will happen: %q", errb)
	}
}

func TestEveryDispositionStatedPasses(t *testing.T) {
	p := bodyFile(t, "Closes #7\nRefs other/repo#9\n")
	rc, out, errb := prbody(t, p, "--repo", "go-zpkg/zbk")
	if rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr: %s", rc, errb)
	}
	for _, want := range []string{"closes  go-zpkg/zbk#7", "refs    other/repo#9", "2 reference(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not carry %q:\n%s", want, out)
		}
	}
}

// A body mentioning nothing is not a failure: most bodies are that case, and a
// check that complained about them would be turned off.
func TestNoReferenceIsNotAFailure(t *testing.T) {
	p := bodyFile(t, "A body with no issue in it at all.\n")
	rc, out, _ := prbody(t, p, "--repo", "go-zpkg/zbk")
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if !strings.Contains(out, "mentions no issue") {
		t.Errorf("output does not say so: %q", out)
	}
}

// ⛔ An unreadable file is 2, not 1: "could not read the body" and "the body is
// wrong" are different answers and this fleet has published a zero that meant
// the first.
func TestAnUnreadableFileIsUnanswerable(t *testing.T) {
	rc, _, errb := prbody(t, filepath.Join(t.TempDir(), "absent.md"), "--repo", "o/r")
	if rc != 2 {
		t.Fatalf("rc = %d, want 2", rc)
	}
	if !strings.Contains(errb, "absent.md") {
		t.Errorf("stderr does not name the file: %q", errb)
	}
}

// Without --repo the parse still works; the bare references simply carry no
// repository, which the output shows rather than inventing one.
func TestWithoutARepositoryItStillAnswers(t *testing.T) {
	p := bodyFile(t, "Refs #7\n")
	if rc, _, _ := prbody(t, p); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
}

func TestArgumentsAreRequired(t *testing.T) {
	if rc, _, _ := prbody(t); rc != 2 {
		t.Errorf("rc = %d with no file, want 2", rc)
	}
}
