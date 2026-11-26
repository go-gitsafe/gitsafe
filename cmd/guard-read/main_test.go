package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var tok = "gh" + "p_" + strings.Repeat("a1B2", 9)

func call(t *testing.T, payload string) (string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	if run(strings.NewReader(payload), &out, &errb, "/nonexistent-home") != 0 {
		t.Fatal("hook must always exit 0")
	}
	return out.String(), errb.String()
}

func TestHook(t *testing.T) {
	dir := t.TempDir()
	sec := filepath.Join(dir, "cli-config.toml")
	os.WriteFile(sec, []byte("token = \""+tok+"\"\n"), 0o600)
	ok := filepath.Join(dir, "README.md")
	os.WriteFile(ok, []byte("hello\n"), 0o600)
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }

	for name, c := range map[string]struct {
		payload string
		deny    bool
	}{
		"read secret":   {`{"tool_name":"Read","tool_input":{"file_path":` + q(sec) + `}}`, true},
		"cat secret":    {`{"tool_name":"Bash","cwd":` + q(dir) + `,"tool_input":{"command":"cat cli-config.toml"}}`, true},
		"read readme":   {`{"tool_name":"Read","tool_input":{"file_path":` + q(ok) + `}}`, false},
		"cat readme":    {`{"tool_name":"Bash","tool_input":{"command":"cat ` + ok + `"}}`, false},
		"other tool":    {`{"tool_name":"Write","tool_input":{"file_path":` + q(sec) + `}}`, false},
		"empty read":    {`{"tool_name":"Read","tool_input":{}}`, false},
		"empty command": {`{"tool_name":"Bash","tool_input":{}}`, false},
		"not json":      {`nope`, false},
	} {
		out, _ := call(t, c.payload)
		if (out != "") != c.deny {
			t.Errorf("%s: output %q", name, out)
		}
		if strings.Contains(out, tok) {
			t.Errorf("%s: the refusal echoed the secret", name)
		}
		if c.deny && !strings.Contains(out, "secretpeek") {
			t.Errorf("%s: refusal does not name the safe tool: %s", name, out)
		}
	}
}

func TestHookFailsOpenAndSaysSo(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	p := filepath.Join(t.TempDir(), "locked")
	os.WriteFile(p, []byte(tok), 0o600)
	os.Chmod(p, 0)
	out, errs := call(t, `{"tool_name":"Read","tool_input":{"file_path":"`+p+`"}}`)
	if out != "" || !strings.Contains(errs, "content NOT checked") {
		t.Fatalf("out=%q err=%q", out, errs)
	}
}

type badReader struct{}

func (badReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestUnreadableInputAndMarshalFailureAllow(t *testing.T) {
	var out, errb bytes.Buffer
	if run(badReader{}, &out, &errb, "/h") != 0 || out.Len() != 0 {
		t.Fatal("unreadable stdin must allow")
	}
	p := filepath.Join(t.TempDir(), "s")
	os.WriteFile(p, []byte("token = \""+tok+"\"\n"), 0o600)
	old := marshal
	marshal = func(any) ([]byte, error) { return nil, errors.New("boom") }
	defer func() { marshal = old }()
	run(strings.NewReader(`{"tool_name":"Read","tool_input":{"file_path":"`+p+`"}}`), &out, &errb, "/h")
	if out.Len() != 0 {
		t.Fatal("a marshal failure must allow, not print half an answer")
	}
	// An empty path or command is nothing to display.
	for _, s := range []string{`{"tool_name":"Read","tool_input":{}}`, `{"tool_name":"Bash","tool_input":{}}`} {
		if o, _ := call(t, s); o != "" {
			t.Fatalf("%s: %s", s, o)
		}
	}
}

func TestReasonByPath(t *testing.T) {
	// covered through the package; here only that the text has no value slot.
	dir := t.TempDir()
	home := dir
	d := filepath.Join(home, ".config", "garm-ci")
	os.MkdirAll(d, 0o700)
	p := filepath.Join(d, "x")
	os.WriteFile(p, []byte("hi"), 0o600)
	var out, errb bytes.Buffer
	run(strings.NewReader(`{"tool_name":"Read","tool_input":{"file_path":"`+p+`"}}`), &out, &errb, home)
	if !strings.Contains(out.String(), "GARM CI credential directory") {
		t.Fatalf("%s", out.String())
	}
}

func TestMain(m *testing.M) {
	osExit = func(int) {}
	os.Exit(m.Run())
}
