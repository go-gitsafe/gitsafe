package literaltick

import (
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ CHANGE THE WITNESS, NOT THE DESIGN.
//
// [FuzzOracle] mutates a whole command, so every input must first build the
// frame — the double quote, the $(, the heredoc opener — before a single body
// byte can matter. That is why two of the three false allows in the
// quoted-heredoc reading came from a human review and only one from the
// fuzzer: the search spends almost all of its budget never reaching the
// function under suspicion.
//
// This target fixes the frame and fuzzes ONLY the body:
//
//	echo --body "$(cat <<'EOF'
//	<BODY>
//	EOF
//	)"
//
// so every input lands on the paren-balance scan. The judgment is the same as
// the oracle's and in the same direction: a shell that evaluates a backtick
// the rule allowed is a failure. Its seeds are the rows the review and the
// shells produced, so the search starts where the known defects were.
func FuzzBody(f *testing.F) {
	shells := oracleShells(f) // bash, dash and zsh, the oracle's own list
	for _, seed := range [][]byte{
		bodySeed("don", "'", "t ", "(", "it", "'", "s", ")", " ", T),
		bodySeed("'", "(", "'", " ", ")", " ", T),
		bodySeed("$'", "a", "\\", "'", "'", " ", ")", " ", T),
		bodySeed("#", "a ", "(", "\n", ")", " ", T),
		bodySeed(";", "#", "a ", "(", "\n", ")", " ", T),
		bodySeed("<<'E'\n", ")", " ", T, "\n", "E\n"),
		bodySeed("(", "a", ")", " ", T),
		bodySeed("`", "a", "`", " ", "'", " ", ")", " ", T),
		bodySeed("\"", "(", "\"", " ", ")", " ", T),
		bodySeed(")", " ", T),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(shells) == 0 || len(data) > 30 {
			t.Skip()
		}
		body := bodyProgram(data)
		// A terminator line of its own in the body would close the frame's
		// heredoc early and make the input a different shape than the one this
		// target is for; FuzzOracle covers those.
		if strings.Contains(body, "\nEOF") || strings.HasPrefix(body, "EOF") {
			t.Skip()
		}
		cmd := "echo --body \"$(cat <<'EOF'\n" + body + "\nEOF\n)\"\n"
		if !strings.Contains(cmd, "`") || Check(cmd).Found() {
			return // nothing to judge, or refused: the safe direction
		}
		for _, sh := range shells {
			if runs(t, sh, cmd) {
				t.Fatalf("%s RAN a backtick of this BODY and the rule allowed it:\n  body %q\n  cmd  %q",
					filepath.Base(sh), body, cmd)
			}
		}
	})
}

// bodyTokens is the grammar of a prose body: the quoting forms, the characters
// that end or hide a paren, and the marker. No command but x and the marker,
// so the worst an input can do is fail to find x.
var bodyTokens = []string{
	"'", "\"", "`", "$'", "\\", "#", "(", ")", ";", "|", "&", " ", "\n", "x",
	"<<'E'\n", "E\n", "don", "t ", "it", "s", "a", "a ", T, T,
}

func bodyProgram(data []byte) string {
	var b strings.Builder
	for _, c := range data {
		b.WriteString(bodyTokens[int(c)%len(bodyTokens)])
	}
	return b.String()
}

func bodySeed(toks ...string) []byte {
	var out []byte
	for _, tok := range toks {
		found := false
		for i, o := range bodyTokens {
			if o == tok {
				out = append(out, byte(i))
				found = true
				break
			}
		}
		if !found {
			panic("not a body token: " + tok)
		}
	}
	return out
}
