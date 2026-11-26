// Package secretread decides whether a file that a tool call is about to
// DISPLAY holds a credential, by opening the file itself and scanning its
// content. It exists because the earlier gate, [secretarg], is NAME-based: on
// 2026-11-26 a subagent printed a whole GARM cli-config.toml (a harmlessly
// named file holding an admin JWT) with a plain `cat`, and again on 2026-10-04
// a token file was printed through awk. A name says nothing about content.
//
// Nothing in this package ever returns, logs or formats a secret VALUE: a
// [Finding] carries a kind and a line number, and a refusal built from one
// cannot echo what it found.
package secretread

import (
	"regexp"
	"sort"
	"strings"
)

// Finding is one secret found in a file. It deliberately has no field for the
// value, so no refusal built from it can print one.
type Finding struct {
	Kind string // e.g. "jwt", "pem-private-key", "github-token"
	Line int    // 1-based; 0 when the format has no lines (JSON)
}

var shapes = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{"pem-private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`)},
	{"github-token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{"gitlab-token", regexp.MustCompile(`\b(?:glpat|gldt|glptt|glrt)-[A-Za-z0-9_-]{16,}`)},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"url-credentials", regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]{3,}@`)},
}

// configExt are the file kinds in which `key = value` is a credential
// assignment; in source code or prose the same text is usually a variable or a
// sentence, and refusing every Go test would make the gate unusable.
var configExt = map[string]bool{
	".toml": true, ".yaml": true, ".yml": true, ".json": true, ".ini": true,
	".cfg": true, ".conf": true, ".env": true, ".properties": true, ".netrc": true,
	"": true,
}

// Scan returns what the content of one file holds, sorted by line then kind.
// ext is the file's extension (with the dot, lowercase) and decides whether the
// generic `secret-named key = long value` rule applies.
func Scan(data []byte, ext string) []Finding {
	var out []Finding
	seen := map[Finding]bool{}
	add := func(f Finding) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for i, line := range strings.Split(string(data), "\n") {
		for _, s := range shapes {
			if s.re.MatchString(line) {
				add(Finding{s.kind, i + 1})
			}
		}
	}
	if configExt[ext] {
		for _, p := range Pairs(data, Detect(ext, data)) {
			if k := PairKind(p.Key, p.Value); k != "" {
				add(Finding{k, p.Line})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// Kinds lists the distinct kinds of fs, in order of first appearance.
func Kinds(fs []Finding) []string {
	var ks []string
	seen := map[string]bool{}
	for _, f := range fs {
		if !seen[f.Kind] {
			seen[f.Kind] = true
			ks = append(ks, f.Kind)
		}
	}
	return ks
}

var secretWords = []string{"token", "secret", "passw", "pwd", "apikey", "api_key",
	"api-key", "access_key", "private_key", "auth_key", "bearer"}

var notSecretSuffix = []string{"_file", "_path", "_url", "_endpoint", "_name", "_ref",
	"_env", "_type", "_length", "_ttl", "_expiry", "_expires", "_id", "_dir", "_header"}

// SecretKey reports whether a key NAME says its value is a credential.
func SecretKey(key string) bool {
	k := strings.ToLower(lastSegment(key))
	for _, s := range notSecretSuffix {
		if strings.HasSuffix(k, s) {
			return false
		}
	}
	for _, w := range secretWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

func lastSegment(key string) string {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		return key[i+1:]
	}
	return key
}

var placeholders = []string{"example", "changeme", "placeholder", "your_", "your-", "xxxx",
	"<", ">", "${", "$(", "%(", "{{", "redacted", "dummy", "null", "none", "true", "false"}

// PairKind returns the kind of secret a key/value pair is, or "". The value is
// judged by shape, never by being compared to anything: a long unbroken string
// under a secret-named key is a secret.
func PairKind(key, value string) string {
	if !SecretKey(key) {
		return ""
	}
	v := value
	if len(v) < 8 || strings.ContainsAny(v, " \t") {
		return ""
	}
	lv := strings.ToLower(v)
	for _, p := range placeholders {
		if strings.Contains(lv, p) {
			return ""
		}
	}
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "./") || strings.HasPrefix(v, "~/") ||
		strings.Contains(v, "://") {
		return ""
	}
	lk := strings.ToLower(lastSegment(key))
	switch {
	case strings.Contains(lk, "application_credential_secret"):
		return "openstack-application-credential-secret"
	case strings.Contains(lk, "passw") || strings.Contains(lk, "pwd"):
		return "password"
	}
	if len(v) < 12 || !mixed(v) && len(v) < 20 {
		return ""
	}
	return "secret-value"
}

// mixed reports whether v has at least two of letters, digits, symbols.
func mixed(v string) bool {
	var l, d, s bool
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			d = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			l = true
		default:
			s = true
		}
	}
	n := 0
	for _, b := range []bool{l, d, s} {
		if b {
			n++
		}
	}
	return n >= 2
}
