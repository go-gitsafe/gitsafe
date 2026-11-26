package secretread

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every secret here is assembled from fragments, so this file holds none and
// is not refused when a session reads it.
var (
	jwt    = "eyJ" + "hbGciOiJIUzI1NiJ9" + "." + "eyJ" + "zdWIiOiJhZG1pbiJ9" + "." + "c2lnbmF0" + "dXJlMTIzNDU2"
	ghTok  = "gh" + "p_" + strings.Repeat("a1B2", 9)
	osSec  = strings.Repeat("Zq7", 13)
	pemHdr = "-----BEGIN " + "RSA PRIVATE KEY" + "-----"
)

var fixtures = []struct {
	name, file, content, kind string // kind "" = must pass
}{
	{"garm cli-config", "cli-config.toml",
		"[manager]\nname = \"local\"\nbase_url = \"http://garm.test:9997\"\ntoken = \"" + jwt + "\"\n", "jwt"},
	{"clouds.yaml", "c.yaml",
		"clouds:\n  os:\n    auth_type: v3applicationcredential\n    auth:\n      auth_url: https://ks.test:5000/v3\n" +
			"      application_credential_id: abc123def456\n      application_credential_secret: " + osSec + "\n",
		"openstack-application-credential-secret"},
	{"pem key", "id.txt", "\n" + pemHdr + "\nMIIE\n-----END RSA PRIVATE KEY-----\n", "pem-private-key"},
	{"raw github token", "tok", ghTok + "\n", "github-token"},
	{"gitlab token", "tok2", "glpat" + "-" + strings.Repeat("ab12", 6), "gitlab-token"},
	{"json secret", "x.json", "{\"auth\":{\"password\":\"" + osSec + "\"}}", "password"},
	{"ini password", "x.ini", "[db]\nhost = h\npassword = hunter2hunter2\n", "password"},
	{"generic secret", "x.env", "API_SECRET=" + osSec + "\n", "secret-value"},
	{"url credentials", "x.cfg", "remote = https://bob:" + "s3cret" + "pw@host.test/repo\n", "url-credentials"},

	{"readme", "README.md", "# Title\n\nSet the token with `token = <your token>` in the config.\n", ""},
	{"go file", "main.go", "package main\n\nfunc f() {\n\ttoken := os.Getenv(\"TOKEN\")\n\tPassword: \"hunter2hunter2\"\n}\n", ""},
	{"config no secrets", "app.toml", "[server]\nhost = \"localhost\"\nport = 8080\ntoken_url = \"https://auth.test/token\"\ntoken_file = \"/run/secrets/t\"\n", ""},
	{"placeholder", "p.yaml", "password: ${DB_PASSWORD}\ntoken: <your-token-here>\nsecret: changeme-please\n", ""},
	{"binary", "b.bin", "\x00\x01" + jwt, ""},
}

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckFileTable(t *testing.T) {
	dir := t.TempDir()
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			v := CheckFile(write(t, dir, f.file, f.content), "/nonexistent-home")
			if f.kind == "" {
				if v.Refused() {
					t.Fatalf("harmless file refused: %v", v.Kinds)
				}
				return
			}
			if !v.Refused() || !contains(v.Kinds, f.kind) {
				t.Fatalf("want kind %q, got %v", f.kind, v.Kinds)
			}
		})
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestSensitivePath(t *testing.T) {
	home := "/h"
	for p, want := range map[string]bool{
		"/h/.config/garm-ci/cli-config.toml": true, "/h/.config/garm-ci": true,
		"/x/clouds.yaml": true, "/x/a.pem": true, "/h/.github-token": true,
		"/h/.ssh/id_ed25519": true, "/h/.ssh/id_ed25519.pub": false,
		"/h/.config/gh/hosts.yml": true, "/x/README.md": false, "/h/.config/other/a": false,
	} {
		if got := SensitivePath(p, home) != ""; got != want {
			t.Errorf("%s: got %v want %v", p, got, want)
		}
	}
}

func TestPublicPemPasses(t *testing.T) {
	p := write(t, t.TempDir(), "ca.pem", "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
	if v := CheckFile(p, "/h"); v.Refused() {
		t.Fatalf("certificate refused: %+v", v)
	}
	p = write(t, t.TempDir(), "k.pem", "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"+pemHdr+"\nx\n")
	if v := CheckFile(p, "/h"); !v.Refused() {
		t.Fatal("pem with a private key passed")
	}
}

func TestPathRefusedWhateverContent(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "garm-ci")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := write(t, dir, "plain.txt", "nothing here\n")
	if v := CheckFile(p, home); v.ByPath == "" {
		t.Fatal("known credential dir not refused by path")
	}
}

func TestCheckFileEdges(t *testing.T) {
	dir := t.TempDir()
	if v := CheckFile(filepath.Join(dir, "absent"), "/h"); v.Err != nil || v.Refused() {
		t.Fatalf("missing file: %+v", v)
	}
	if v := CheckFile(dir, "/h"); v.Err != nil || v.Refused() {
		t.Fatalf("directory: %+v", v)
	}
	big := write(t, dir, "big.txt", strings.Repeat("x", MaxBytes+10)+"\n"+ghTok)
	if v := CheckFile(big, "/h"); v.Refused() {
		t.Fatal("content past the bound was read")
	}
	if os.Getuid() != 0 {
		p := write(t, dir, "locked", ghTok)
		os.Chmod(p, 0)
		if v := CheckFile(p, "/h"); v.Err == nil || v.Refused() {
			t.Fatalf("unreadable file must fail open with Err: %+v", v)
		}
	}
}

func TestCommands(t *testing.T) {
	dir := t.TempDir()
	sec := write(t, dir, "cli-config.toml", fixtures[0].content)
	ok := write(t, dir, "README.md", "# hi\n")
	for cmd, want := range map[string]bool{
		"cat " + sec:                        true,
		"head -n 3 " + sec:                  true,
		"tail -5 " + sec:                    true,
		"grep -n base_url " + sec:           true,
		"rg base_url " + sec:                true,
		"grep -c token " + sec:              false,
		"grep -q token " + sec:              false,
		"grep -l token " + sec:              false,
		"sed -n 1p " + sec:                  true,
		"sed -i s/a/b/ " + sec:              false,
		"awk '{print $1}' " + sec:           true,
		"cat < " + sec:                      true,
		"cat <" + sec:                       true,
		"jq . " + sec:                       true,
		"strings " + sec:                    true,
		"base64 " + sec:                     true,
		"FOO=1 cat " + sec:                  true,
		"sudo cat " + sec:                   true,
		"cat " + ok + " " + sec:             true,
		"cat " + ok + " | head -3":          false,
		"echo hi && cat " + sec:             true,
		"cat " + filepath.Join(dir, "cli*"): true,
		"cat \"" + sec + "\"":               true,
		"cat " + ok:                         false,
		"echo " + sec:                       false,
		"ls " + sec:                         false,
		"cp " + sec + " /dev/null":          false,
		"cat > " + filepath.Join(dir, "o"):  false,
		"cat $(echo " + sec + ")":           false,
		"":                                  false,
	} {
		r, _ := CheckCommand(cmd, dir, "/h")
		if (r != nil) != want {
			t.Errorf("%q: refused=%v want %v", cmd, r != nil, want)
		}
	}
	r, _ := CheckCommand("cat cli-config.toml", dir, "/h")
	if r == nil {
		t.Fatal("relative operand not resolved against cwd")
	}
}

func TestDirectoryOfCredentials(t *testing.T) {
	home := t.TempDir()
	d := filepath.Join(home, ".config", "garm-ci")
	os.MkdirAll(d, 0o700)
	if r, _ := CheckCommand("grep -r base_url "+d, "/", home); r == nil || r.ByPath == "" {
		t.Fatal("recursive grep over a credential dir not refused")
	}
	if r, _ := CheckCommand("cat ~/.config/garm-ci/x", "/", home); r != nil {
		t.Fatal("absent file refused") // nothing to display
	}
}

func TestPeekPrintsNoValue(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "cli-config.toml", fixtures[0].content)
	var b bytes.Buffer
	if err := Peek(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, leak := range []string{jwt, "eyJ", "garm.test:9997/", "local"} {
		if strings.Contains(out, leak) {
			t.Fatalf("peek leaked %q:\n%s", leak, out)
		}
	}
	for _, want := range []string{"format: toml", "mode:   0600", "manager.token: <secret jwt, ", "manager.base_url: <url http://garm.test:9997>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	raw := write(t, dir, "tok", ghTok)
	b.Reset()
	if err := Peek(&b, raw); err != nil || strings.Contains(b.String(), ghTok) || !strings.Contains(b.String(), "github-token") {
		t.Fatalf("raw peek: %v\n%s", err, b.String())
	}
	if Peek(&b, dir) == nil || Peek(&b, filepath.Join(dir, "absent")) == nil {
		t.Fatal("peek of a directory or absent file must fail")
	}
}

func TestKey(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "cli-config.toml", fixtures[0].content)
	if v, err := Key(p, "base_url"); err != nil || v != "http://garm.test:9997" {
		t.Fatalf("base_url: %q %v", v, err)
	}
	if v, err := Key(p, "manager.base_url"); err != nil || v == "" {
		t.Fatalf("dotted key: %q %v", v, err)
	}
	for _, k := range []string{"token", "manager.token", "nope"} {
		if v, err := Key(p, k); err == nil || v != "" {
			t.Fatalf("key %q must be refused: %q", k, v)
		}
	}
	// A non-secret NAME with a secret-shaped VALUE is refused too.
	q := write(t, dir, "q.toml", "url = \""+ghTok+"\"\n")
	if _, err := Key(q, "url"); err == nil {
		t.Fatal("secret-shaped value under an allowed key was printed")
	}
	if _, err := Key(filepath.Join(dir, "absent"), "url"); err == nil {
		t.Fatal("absent file")
	}
}

func TestFormats(t *testing.T) {
	if Detect("", []byte("{\"a\":1}")) != "json" || Detect("", []byte("a: b\n")) != "yaml" ||
		Detect("", []byte("a = 1\n")) != "toml" || Detect("", []byte("plain words\n")) != "raw" ||
		Detect(".conf", nil) != "ini" {
		t.Fatal("Detect")
	}
	ps := Pairs([]byte("a:\n  b:\n    c: 1\n  d: 2\ne: 3\n"), "yaml")
	got := []string{}
	for _, p := range ps {
		got = append(got, p.Key)
	}
	if strings.Join(got, ",") != "a.b.c,a.d,e" {
		t.Fatalf("yaml nesting: %v", got)
	}
	if Pairs([]byte("{bad"), "json") != nil || Pairs([]byte("x"), "raw") != nil {
		t.Fatal("bad json/raw")
	}
	j := Pairs([]byte(`{"a":[{"b":"x"}],"n":1,"z":null}`), "json")
	if len(j) != 2 || j[0].Key != "a.0.b" {
		t.Fatalf("json: %+v", j)
	}
	ini := Pairs([]byte("[s]\nk = v # c\n\"q\" = 'w'\nx := 1\n"), "ini")
	if len(ini) != 2 || ini[0].Key != "s.k" || ini[0].Value != "v" || ini[1].Value != "w" {
		t.Fatalf("ini: %+v", ini)
	}
}

func FuzzScan(f *testing.F) {
	for _, x := range fixtures {
		f.Add([]byte(x.content), filepath.Ext(x.file))
	}
	f.Fuzz(func(t *testing.T, data []byte, ext string) {
		for _, fd := range Scan(data, ext) {
			if fd.Kind == "" || fd.Line < 0 {
				t.Fatal("bad finding")
			}
		}
		for _, fm := range []string{"json", "toml", "yaml", "ini", "raw"} {
			Pairs(data, fm)
		}
		var b bytes.Buffer
		for _, p := range Pairs(data, Detect(ext, data)) {
			b.WriteString(Class(p.Key, p.Value))
		}
	})
}

func FuzzOperands(f *testing.F) {
	for _, s := range []string{"cat a", "grep -n x f | head", "sed -n 1p < f", "awk '{print}' f", "a=b cat 'x y'", "cat \"", "<", "cat <<EOF\nx\nEOF"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { Operands(s) })
}
