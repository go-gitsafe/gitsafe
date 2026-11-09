package bigblob

import (
	"strings"
	"testing"
)

const (
	a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	c = "cccccccccccccccccccccccccccccccccccccccc"
	d = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" // SHA-256
)

func TestObjectsKeepsOrderAndFirstPath(t *testing.T) {
	in := a + "\n" + // a commit: no path
		b + " quake-wasmbox\n" +
		c + " cmd/x/main.go\n" +
		b + " other/name\n" + // same blob again: the first path stays
		"not-a-sha path\n" +
		d + " big.bin\n"
	shas, path := Objects(strings.NewReader(in))
	if strings.Join(shas, ",") != a+","+b+","+c+","+d {
		t.Fatalf("shas %v", shas)
	}
	if path[a] != "" || path[b] != "quake-wasmbox" || path[c] != "cmd/x/main.go" || path[d] != "big.bin" {
		t.Fatalf("paths %v", path)
	}
}

func TestOverKeepsOnlyLargeBlobs(t *testing.T) {
	path := map[string]string{b: "quake-wasmbox", c: "small.go", d: "big.bin"}
	in := "commit " + a + " 250\n" +
		"blob " + b + " 14504439\n" + // the binary of go-quake1/engine#79
		"blob " + c + " 1200\n" +
		"blob " + d + " 1048576\n" + // exactly the limit: refused
		"tree " + a + " 99999999\n" + // not a blob
		"blob " + b + " notanumber\n" +
		"garbage\n"
	got := Over(strings.NewReader(in), path, Limit)
	if len(got) != 2 || got[0].Path != "quake-wasmbox" || got[0].Size != 14504439 || got[1].Path != "big.bin" {
		t.Fatalf("got %+v", got)
	}
	if s := got[0].String(); !strings.Contains(s, "13.8 MiB") || !strings.Contains(s, "quake-wasmbox") || !strings.Contains(s, b[:12]) {
		t.Fatalf("line %q", s)
	}
	if s := (Blob{SHA: b, Size: 2048}).String(); !strings.Contains(s, "(no path)") || !strings.Contains(s, "2.0 KiB") {
		t.Fatalf("line %q", s)
	}
}

func TestIsSHA(t *testing.T) {
	for s, want := range map[string]bool{a: true, d: true, "ABC": false, strings.ToUpper(a): false, a[:39] + "g": false} {
		if isSHA(s) != want {
			t.Errorf("isSHA(%q) = %v", s, !want)
		}
	}
	if short("abc") != "abc" {
		t.Error("short of a short id")
	}
}
