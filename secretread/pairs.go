package secretread

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Pair is one key/value found in a config file. Key is the dotted path
// (section.key, or the YAML nesting).
type Pair struct {
	Key, Value string
	Line       int
}

var (
	sectionRe = regexp.MustCompile(`^\[\[?\s*([^\[\]]+?)\s*\]\]?\s*(?:[#;].*)?$`)
	tomlKeyRe = regexp.MustCompile(`(?m)^\s*[A-Za-z0-9_."'-]+\s*=`)
	yamlKeyRe = regexp.MustCompile(`(?m)^\s*(?:- )?[A-Za-z0-9_."'-]+:(?:\s|$)`)
)

// Detect names the format: json, toml, yaml, ini or raw. The extension decides
// when it is one of the well known ones; otherwise the content is sniffed.
func Detect(ext string, data []byte) string {
	switch ext {
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".yaml", ".yml":
		return "yaml"
	case ".ini", ".cfg", ".conf", ".properties":
		return "ini"
	}
	t := strings.TrimSpace(string(data))
	if (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t)) {
		return "json"
	}
	if tomlKeyRe.Match(data) {
		return "toml"
	}
	if yamlKeyRe.Match(data) {
		return "yaml"
	}
	return "raw"
}

// Pairs extracts the key/value pairs of a config file. It is a line reader, not
// a validating parser: a malformed file yields what it can, never an error,
// because a gate must keep reading past what a strict parser would refuse.
func Pairs(data []byte, format string) []Pair {
	if format == "json" {
		var v any
		if json.Unmarshal(data, &v) != nil {
			return nil
		}
		var out []Pair
		flatten("", v, &out)
		return out
	}
	if format == "raw" {
		return nil
	}
	type frame struct {
		indent int
		key    string
	}
	var (
		out     []Pair
		section string
		stack   []frame
	)
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		t := strings.TrimSpace(line)
		if t == "" || t[0] == '#' || t[0] == ';' || strings.HasPrefix(t, "//") {
			continue
		}
		if m := sectionRe.FindStringSubmatch(t); m != nil && format != "yaml" {
			section = m[1]
			continue
		}
		t = strings.TrimPrefix(t, "- ")
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		k, v, ok := splitPair(t)
		if !ok {
			continue
		}
		prefix := section
		if format == "yaml" {
			for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
				stack = stack[:len(stack)-1]
			}
			parts := make([]string, 0, len(stack)+1)
			for _, f := range stack {
				parts = append(parts, f.key)
			}
			prefix = strings.Join(parts, ".")
			if v == "" {
				stack = append(stack, frame{indent, k})
				continue
			}
		}
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		out = append(out, Pair{Key: full, Value: v, Line: i + 1})
	}
	return out
}

// splitPair splits `key = value` or `key: value`; a `:=` is Go, not config.
func splitPair(t string) (k, v string, ok bool) {
	idx := -1
	inQ := byte(0)
	for i := 0; i < len(t); i++ {
		c := t[i]
		if inQ != 0 {
			if c == inQ {
				inQ = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQ = c
			continue
		}
		if c == '=' && !(i > 0 && (t[i-1] == ':' || t[i-1] == '!' || t[i-1] == '=')) && !(i+1 < len(t) && t[i+1] == '=') {
			idx = i
			break
		}
		if c == ':' && (i+1 == len(t) || t[i+1] == ' ' || t[i+1] == '\t') {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return "", "", false
	}
	k = unquote(strings.TrimSpace(t[:idx]))
	if k == "" || strings.ContainsAny(k, " \t") {
		return "", "", false
	}
	return k, scalar(strings.TrimSpace(t[idx+1:])), true
}

// scalar takes a quoted string whole, or a bare value up to a ` #` comment.
func scalar(v string) string {
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : end+1]
		}
		return v[1:]
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimRight(strings.TrimSpace(v), ",")
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func flatten(prefix string, v any, out *[]Pair) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			flatten(join(k), x[k], out)
		}
	case []any:
		for i, e := range x {
			flatten(join(strconv.Itoa(i)), e, out)
		}
	case string:
		*out = append(*out, Pair{Key: prefix, Value: x})
	case nil:
	default:
		b, _ := json.Marshal(x)
		*out = append(*out, Pair{Key: prefix, Value: string(b)})
	}
}
