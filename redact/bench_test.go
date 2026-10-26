package redact

import (
	"os"
	"strings"
	"testing"
)

// benchPrompt is the 2 KB prompt of the go-llmflow gateway benchmark
// (gwbench): French prose with an e-mail address, an IBAN, a phone number and
// a fake GitHub token. The token is assembled here so that no token-shaped
// string is committed.
func benchPrompt(tb testing.TB) string {
	b, err := os.ReadFile("testdata/prompt-2k.txt")
	if err != nil {
		tb.Fatal(err)
	}
	return strings.Replace(string(b), "{{TOKEN}}", "ghp_"+strings.Repeat("0", 36), 1)
}

func BenchmarkFindPrompt(b *testing.B) {
	r := New()
	text := benchPrompt(b)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		r.Find(text)
	}
}

func BenchmarkStringPrompt(b *testing.B) {
	r := New()
	text := benchPrompt(b)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		r.String(text)
	}
}

func TestPromptFinding(t *testing.T) {
	text := benchPrompt(t)
	got := New().Find(text)
	if len(got) != 1 || got[0].Kind != Token || !strings.HasPrefix(text[got[0].Start:], "ghp_") || got[0].End-got[0].Start != 40 {
		t.Fatalf("%+v", got)
	}
}
