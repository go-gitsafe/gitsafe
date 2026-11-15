package literaltick

import (
	"fmt"
	"strings"
	"testing"
)

func TestExploreTmp(t *testing.T) {
	var sb strings.Builder
	for _, body := range []string{
		"#)\";#\"\n" + T,
		"#)\";\"\n" + T,
		"#)\"a b\"\n" + T,
		"#)\"a;b\"\n" + T,
		"#)\"a#b\"\n" + T,
		"#)\"#\"\n" + T,
		"# ) \"a b\"\n" + T,
		"#)\"a\"\n" + T,
		"#)\"a\" " + T,
		"#)\"a;#b\"\n" + T,
		"#)\";#\"" + T,
		"# note ) \";#\"\n" + T,
		"#)\"x;#\"\nx\n" + T,
		"#)\"(#\"\n" + T,
		"#)\"(\"\n" + T,
		"#)\" #\"\n" + T,
		"#)\",#\"\n" + T,
	} {
		cmd := "echo --body \"$(cat <<'EOF'\n" + body + "\nEOF\n)\"\n"
		fmt.Fprintf(&sb, "%-40q rule-refuses=%v", body, check(cmd).Found())
		for _, sh := range oracleShells(t) {
			fmt.Fprintf(&sb, " %s=%v", sh[strings.LastIndex(sh, "/")+1:], runs(t, sh, cmd))
		}
		sb.WriteString("\n")
	}
	t.Log("\n" + sb.String())
}
