package ghbyhand

import "testing"

func TestIsActionsEnablePath(t *testing.T) {
	for path, want := range map[string]bool{
		"repos/o/r/actions/permissions":         true,
		"/repos/o/r/actions/permissions/":       true,
		"repos/o/r/actions/workflows/12/enable": true,
		// every segment matters
		"orgs/o/r/actions/permissions":              false,
		"repos/o/r/other/permissions":               false,
		"repos/o/r/actions/other":                   false,
		"repos/o/r/actions/workflows/12/disable":    false,
		"repos/o/r/actions/other/12/enable":         false,
		"repos/o/r/other/workflows/12/enable":       false,
		"orgs/o/r/actions/workflows/12/enable":      false,
		"repos/o/r/actions":                         false,
		"repos/o/r/actions/permissions/selected":    false,
		"repos/o/r/actions/workflows/12":            false,
		"repos/o/r/actions/workflows/12/enable/now": false,
		"": false,
	} {
		if got := isActionsEnablePath(path); got != want {
			t.Errorf("isActionsEnablePath(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestIsRepoPath(t *testing.T) {
	for path, want := range map[string]bool{
		"repos/o/r": true, "/repos/o/r/": true,
		"repos/o": false, "repos/o/r/issues": false, "orgs/o/r": false, "repos//r": false, "repos/o/": false,
	} {
		if got := isRepoPath(path); got != want {
			t.Errorf("isRepoPath(%q) = %t, want %t", path, got, want)
		}
	}
}
