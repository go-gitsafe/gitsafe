package main

import (
	"strings"
	"testing"
)

const issuesHelp = "Usage: ghmerge [flags]\n      --issues-waiver string   merge anyway\n"

func TestIssuesWaiverIsPassedToGhmerge(t *testing.T) {
	g := newWaiveMain()
	g.script = [][]string{{run1("ci", "completed", "success")}}
	g.help = issuesHelp
	if code, _, errs := waiveScenario(t, g, "--issues-waiver", "tracking issue"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := strings.Join(g.mergeFlags, "|"); got != "--issues-waiver|tracking issue" {
		t.Errorf("ghmerge got %q", got)
	}
}

func TestEmptyIssuesWaiverIsRefusedBeforeAnyCall(t *testing.T) {
	g := newWaiveMain()
	g.help = issuesHelp
	code, _, errs := waiveScenario(t, g, "--issues-waiver", " ")
	if code != 2 || g.requests != 0 || g.ran != 0 || !strings.Contains(errs, "needs a reason") {
		t.Errorf("exit %d, %d requests: %s", code, g.requests, errs)
	}
}

func TestOldGhmergeWithoutTheGateIsRefusedBeforeAnyCall(t *testing.T) {
	g := newWaiveMain() // help lists only --waive
	code, _, errs := waiveScenario(t, g, "--issues-waiver", "why")
	if code != 1 || g.requests != 0 || g.ran != 0 || !strings.Contains(errs, "no issues gate") {
		t.Errorf("exit %d, %d requests: %s", code, g.requests, errs)
	}
}

// ghmerge exit 3: merged, but an issue could not be closed. The merge commit
// exists, so the tag is cut; the status still says the job is unfinished.
func TestMergedButIssueOpenStillTagsAndExitsNonZero(t *testing.T) {
	g := newWaiveMain()
	g.script = [][]string{{run1("ci", "completed", "success")}}
	g.mergeErr = errMergedIssuesOpen
	code, _, errs := waiveScenario(t, g)
	if code != 1 || !g.tagged() || !strings.Contains(errs, "the run will exit 1") {
		t.Errorf("exit %d, tagged %v: %s", code, g.tagged(), errs)
	}
}

func TestAnyOtherMergeErrorStillStopsBeforeTagging(t *testing.T) {
	g := newWaiveMain()
	g.mergeErr = errTest("refused")
	if code, _, _ := waiveScenario(t, g); code != 1 || g.tagged() {
		t.Errorf("exit %d, tagged %v", code, g.tagged())
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
