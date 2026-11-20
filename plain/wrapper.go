// Copyright (c) the go-gitsafe authors.
// SPDX-License-Identifier: BSD-3-Clause

package plain

import "strings"

// ProductionSetopt is the option line the Bash tool runs before every command
// here, captured from a live `ps -ww -o command= -p $$` on 2026-11-19 (zsh 5.9):
//
//	/bin/zsh -c source <snapshot> 2>/dev/null || true && \builtin setopt
//	NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL NO_BRACE_CCL NO_MAGIC_EQUAL_SUBST
//	2>/dev/null || true && … && eval '<command>' && pwd -P >| <file>
//
// ⛔ PINNED, NOT REMEMBERED (#156): testdata/production-wrapper.txt holds the same
// line and TestTheProductionWrapperIsPinned compares the two, so a change to
// either fails CI by name. The wrapper is not visible from CI: when the tool's
// wrapper changes, re-capture it with ps and update both.
const ProductionSetopt = `\builtin setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL NO_BRACE_CCL NO_MAGIC_EQUAL_SUBST 2>/dev/null || true`

// ProductionScript is the zsh -c argument that runs cmd the way the tool does: an
// empty snapshot, the setopt line, then eval of the single-quoted command. It is
// what every oracle in this repository asks zsh with.
func ProductionScript(cmd string) string {
	q := "'" + strings.ReplaceAll(cmd, "'", `'\''`) + "'"
	return "source /dev/null 2>/dev/null || true && " + ProductionSetopt + " && eval " + q
}
