package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var jwt = "eyJ" + "hbGciOiJIUzI1NiJ9" + "." + "eyJ" + "zdWIiOiJhZG1pbiJ9" + "." + "c2lnbmF0" + "dXJlMTIzNDU2"

func TestRun(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cli-config.toml")
	os.WriteFile(p, []byte("base_url = \"http://garm.test:9997\"\ntoken = \""+jwt+"\"\n"), 0o600)
	var out, errb bytes.Buffer
	if run([]string{p}, &out, &errb) != 0 || strings.Contains(out.String(), jwt) || !strings.Contains(out.String(), "<secret jwt") {
		t.Fatalf("%s %s", out.String(), errb.String())
	}
	out.Reset()
	if run([]string{"--key", "base_url", p}, &out, &errb) != 0 || strings.TrimSpace(out.String()) != "http://garm.test:9997" {
		t.Fatalf("%q", out.String())
	}
	out.Reset()
	errb.Reset()
	if run([]string{"--key", "token", p}, &out, &errb) == 0 || out.Len() != 0 || strings.Contains(errb.String(), jwt) {
		t.Fatalf("key token: %q %q", out.String(), errb.String())
	}
	if run(nil, &out, &errb) == 0 || run([]string{filepath.Join(t.TempDir(), "absent")}, &out, &errb) == 0 {
		t.Fatal("bad usage must exit 1")
	}
}

func TestMain(m *testing.M) {
	osExit = func(int) {}
	os.Exit(m.Run())
}
