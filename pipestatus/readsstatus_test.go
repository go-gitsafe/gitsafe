package pipestatus

import "testing"

// ⛔ ONE NOTION OF "THIS STATEMENT CARRIES A STATUS". bgstatus asks this
// function rather than keeping a copy, because the copy's tempting shortcut —
// "a $? inside quotes is a mention" — is wrong for DOUBLE quotes, and
// `echo "rc=$?"` is the spelling that was actually typed, three times on
// 2026-10-11 and twice on 2026-11-22.
func TestReadsStatusIsOneNotionAndKnowsWhichQuotesExpand(t *testing.T) {
	for st, want := range map[string]bool{
		`echo "rc=$?"`:          true,
		`echo rc=$?`:            true,
		`exit $?`:               true,
		`code=$?`:               true,
		`if [ $? -ne 0 ]`:       true,
		`echo "${?}"`:           true,
		`echo 'rc=$?'`:          false,
		`echo rc=ok`:            false,
		`echo 'write $? here' `: false,
		``:                      false,
	} {
		if got := ReadsStatus(st); got != want {
			t.Errorf("ReadsStatus(%q) = %v, want %v", st, got, want)
		}
	}
}
