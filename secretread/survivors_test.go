package secretread

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPairKindBoundaries(t *testing.T) {
	for _, c := range []struct{ key, val, want string }{
		{"host", "abcdefghijkl12345", ""},                 // not a secret-named key
		{"password", "short", ""},                         // under 8
		{"password", "has space in it", ""},               // not one unbroken value
		{"password", "hunter22", "password"},              // exactly 8
		{"token", "/run/secrets/abcdefg12345", ""},        // a path
		{"token", "./secrets/abcdefg12345", ""},           // a path
		{"token", "~/secrets/abcdefg12345", ""},           // a path
		{"token", "https://t.test/abcdefg12345", ""},      // a URL
		{"token", "abcdefghij", ""},                       // 10 letters
		{"token", "abcdefghijkl", ""},                     // 12 letters, one class, under 20
		{"token", "abcdefghijklmnopqrst", "secret-value"}, // 20 letters
		{"token", "abcdef123456", "secret-value"},         // 12, two classes
		{"token", "a1b2c3d4e5", ""},                       // 10, two classes
		{"token_file", "abcdef123456", ""},                // a file NAME, not the secret
		{"db.pwd", "hunter22", "password"},
		{"application_credential_secret", "abcdef123456", "openstack-application-credential-secret"},
	} {
		if got := PairKind(c.key, c.val); got != c.want {
			t.Errorf("PairKind(%q, %q) = %q, want %q", c.key, c.val, got, c.want)
		}
	}
	for k, want := range map[string]bool{"token_url": false, "db_token_path": false, "api_token": true, "a.b.password": true, "region": false} {
		if SecretKey(k) != want {
			t.Errorf("SecretKey(%q) != %v", k, want)
		}
	}
}

func TestScanOrderedByLine(t *testing.T) {
	data := []byte("password = \"hunter22\"\n" + jwt + "\n")
	fs := Scan(data, ".toml")
	if len(fs) != 2 || fs[0].Line != 1 || fs[1].Line != 2 {
		t.Fatalf("not sorted by line: %+v", fs)
	}
	// Same line: ordered by kind.
	data = []byte("password = \"" + ghTok + "\"\n")
	fs = Scan(data, ".toml")
	if len(fs) < 2 || fs[0].Kind > fs[1].Kind {
		t.Fatalf("not sorted by kind within a line: %+v", fs)
	}
}

func TestClassOfShortSecretNamedValue(t *testing.T) {
	if got := Class("api_token", "abc"); got != "<secret value, 3 bytes>" {
		t.Fatal(got)
	}
	if got := Class("host", "h"); got != "<string 1 bytes>" {
		t.Fatal(got)
	}
}

func TestQuietGrepLongOptions(t *testing.T) {
	dir := t.TempDir()
	sec := write(t, dir, "s.toml", fixtures[0].content)
	for _, o := range []string{"--count", "--quiet", "--silent", "--files-with-matches", "--files-without-match"} {
		if r, _ := CheckCommand("grep "+o+" token "+sec, dir, "/h"); r != nil {
			t.Errorf("grep %s prints no content but was refused", o)
		}
	}
	if r, _ := CheckCommand("grep --color=never token "+sec, dir, "/h"); r == nil {
		t.Error("grep --color prints content")
	}
	if r, _ := CheckCommand("a-b=1 cat "+sec, dir, "/h"); r != nil {
		t.Error("`a-b=1` is not an assignment: the line runs a program of that name")
	}
	if r, _ := CheckCommand("A_b1=1 cat "+sec, dir, "/h"); r == nil {
		t.Error("a real assignment prefix hid the file")
	}
}

func TestGlobBound(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 70; i++ {
		write(t, dir, fmt.Sprintf("f%02d", i), "plain\n")
	}
	late := write(t, dir, "f99", ghTok)
	if r, _ := CheckCommand("cat "+filepath.Join(dir, "f*"), dir, "/h"); r != nil {
		t.Fatalf("scanned past the 64-file bound: %s", late)
	}
	os.Remove(filepath.Join(dir, "f00"))
	write(t, dir, "a", ghTok)
	if r, _ := CheckCommand("cat "+filepath.Join(dir, "?"), dir, "/h"); r == nil {
		t.Fatal("secret inside the bound missed")
	}
}

func TestPairsEdges(t *testing.T) {
	if Pairs([]byte("a = 1\n"), "raw") != nil {
		t.Fatal("raw has no pairs")
	}
	if ps := Pairs([]byte("hello world = 1\n= 2\nk: v\n"), "ini"); len(ps) != 1 || ps[0].Key != "k" {
		t.Fatalf("a key with a space is prose: %+v", ps)
	}
	ps := Pairs([]byte("[s]\n\"q\" = 'w'\n'z' = 1\n\"\" = 3\n"), "ini")
	if len(ps) != 2 || ps[0].Key != "s.q" || ps[1].Key != "s.z" {
		t.Fatalf("quoted keys: %+v", ps)
	}
	if got := unquote("\""); got != "\"" {
		t.Fatalf("a lone quote: %q", got)
	}
	if got := unquote("'ab\""); got != "'ab\"" {
		t.Fatalf("mismatched quotes: %q", got)
	}
}

func TestErrorsAreTheRightOnes(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "absent")
	var b bytes.Buffer
	if err := Peek(&b, absent); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("peek of an absent file: %v", err)
	}
	if err := Peek(&b, dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("peek of a directory: %v", err)
	}
	if _, err := Key(absent, "url"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("key of an absent file: %v", err)
	}
	if _, err := readBounded(absent); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("readBounded: %v", err)
	}
	// A key off the list is refused BY NAME, whatever its value.
	p := write(t, dir, "c.toml", "token = \"short\"\nregion = \"r1\"\n")
	if _, err := Key(p, "token"); err == nil || !strings.Contains(err.Error(), "non-secret list") {
		t.Fatalf("token: %v", err)
	}
	if v, err := Key(p, "region"); err != nil || v != "r1" {
		t.Fatalf("region: %q %v", v, err)
	}
	if os.Getuid() != 0 {
		l := write(t, dir, "locked", "url = \"x\"\n")
		os.Chmod(l, 0)
		if err := Peek(&b, l); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("peek of an unreadable file: %v", err)
		}
		if _, err := Key(l, "url"); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("key of an unreadable file: %v", err)
		}
		if v := CheckFile(l, "/h"); !errors.Is(v.Err, fs.ErrPermission) {
			t.Fatalf("check of an unreadable file: %v", v.Err)
		}
	}
	// A stat error that is not "absent" is an Err, not silence.
	if v := CheckFile("a\x00b", "/h"); v.Err == nil || v.Refused() {
		t.Fatalf("bad path: %+v", v)
	}
	if err := Peek(&b, "a\x00b"); err == nil {
		t.Fatal("peek of a bad path")
	}
}

func TestDegenerateShellText(t *testing.T) {
	for _, s := range []string{"", "FOO=1", "sudo", "cat", "grep", "cat <", "echo >", "a= b"} {
		if r, _ := CheckCommand(s, "/", "/h"); r != nil {
			t.Errorf("%q refused", s)
		}
	}
	if ps := Pairs([]byte("a:\n"), "yaml"); len(ps) != 0 {
		t.Fatalf("a bare key holds no value: %+v", ps)
	}
	if ps := Pairs([]byte("=\n:\n"), "ini"); len(ps) != 0 {
		t.Fatalf("empty keys: %+v", ps)
	}
}
