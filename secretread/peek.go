package secretread

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// NonSecretKeys are the key names whose value may be shown by --key. A key
// outside this list is never printed, whatever its value looks like.
var NonSecretKeys = map[string]bool{
	"url": true, "base_url": true, "auth_url": true, "api_url": true, "endpoint": true,
	"host": true, "hostname": true, "port": true, "username": true, "user": true,
	"user_name": true, "region": true, "region_name": true, "project_id": true,
	"project_name": true, "domain_name": true, "user_domain_name": true,
	"project_domain_name": true, "auth_type": true, "interface": true,
	"identity_api_version": true, "application_credential_id": true, "name": true,
	"email": true, "insecure": true, "verify": true, "cacert": true,
}

// Class describes a value without revealing it.
func Class(key, value string) string {
	if k := valueKind(key, value); k != "" {
		return fmt.Sprintf("<secret %s, %d bytes>", k, len(value))
	}
	last := strings.ToLower(lastSegment(key))
	if NonSecretKeys[last] {
		if u, err := url.Parse(value); err == nil && u.Scheme != "" && u.Host != "" && u.User == nil {
			return "<url " + u.Scheme + "://" + u.Host + ">"
		}
	}
	if SecretKey(key) {
		return fmt.Sprintf("<secret value, %d bytes>", len(value))
	}
	return fmt.Sprintf("<string %d bytes>", len(value))
}

func valueKind(key, value string) string {
	for _, s := range shapes {
		if s.re.MatchString(value) {
			return s.kind
		}
	}
	return PairKind(key, value)
}

// Peek writes the properties of one file: mode, size, format and its keys with
// values replaced by a class. It never writes a value.
func Peek(w io.Writer, path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	data, err := readBounded(path)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(path))
	format := Detect(ext, data)
	fmt.Fprintf(w, "file:   %s\nmode:   %04o\nsize:   %d bytes\nformat: %s\n", path, st.Mode().Perm(), st.Size(), format)
	if st.Size() > MaxBytes {
		fmt.Fprintf(w, "note:   only the first %d bytes were read\n", MaxBytes)
	}
	pairs := Pairs(data, format)
	if len(pairs) == 0 {
		fs := Scan(data, ext)
		fmt.Fprintf(w, "lines:  %d\n", strings.Count(string(data), "\n")+1)
		if k := Kinds(fs); len(k) > 0 {
			fmt.Fprintf(w, "secret: %s (first at line %d)\n", strings.Join(k, ", "), fs[0].Line)
		} else {
			fmt.Fprintln(w, "secret: none recognised")
		}
		return nil
	}
	for _, p := range pairs {
		fmt.Fprintf(w, "%s: %s\n", p.Key, Class(p.Key, p.Value))
	}
	return nil
}

// Key returns the value of one key, only if the key is on the non-secret list
// AND the value has no secret shape.
func Key(path, key string) (string, error) {
	last := strings.ToLower(lastSegment(key))
	if !NonSecretKeys[last] {
		return "", fmt.Errorf("key %q is not on the non-secret list; refusing to print it", key)
	}
	data, err := readBounded(path)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, p := range Pairs(data, Detect(ext, data)) {
		if p.Key != key && lastSegment(p.Key) != key {
			continue
		}
		if valueKind(p.Key, p.Value) != "" {
			return "", fmt.Errorf("key %q holds a secret-shaped value; refusing to print it", key)
		}
		return p.Value, nil
	}
	return "", fmt.Errorf("key %q not found in %s", key, path)
}

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxBytes))
}
