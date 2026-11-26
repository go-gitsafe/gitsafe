package secretread

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes bounds how much of one file is read to scan it.
const MaxBytes = 1 << 20

// Verdict is the answer for one file. Kinds never holds a value.
type Verdict struct {
	Path   string
	Kinds  []string // what the content holds
	Line   int      // first line of a finding, 0 if none or no lines
	ByPath string   // non-empty: refused because the PATH is a known credential file
	Err    error    // the file could not be read: the caller fails OPEN and says so
}

// Refused reports whether the file must not be displayed.
func (v Verdict) Refused() bool { return v.ByPath != "" || len(v.Kinds) > 0 }

// Expand turns a path the way a shell would see it into an absolute one:
// ~, $HOME and ${HOME} become home, a relative path is taken from cwd.
func Expand(p, home, cwd string) string {
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/"):
		p = filepath.Join(home, p[2:])
	}
	p = strings.NewReplacer("${HOME}", home, "$HOME", home).Replace(p)
	if !filepath.IsAbs(p) && cwd != "" {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// SensitivePath names why a PATH alone is a credential file ("" if it is not).
// Belt and braces: the content scan is the gate, this catches a file whose
// content the scan does not recognise.
func SensitivePath(p, home string) string {
	p = filepath.Clean(p)
	base := filepath.Base(p)
	rel := ""
	if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
		rel = filepath.ToSlash(p[len(home)+1:])
	}
	switch {
	case strings.HasPrefix(rel, ".config/garm-ci/") || rel == ".config/garm-ci":
		return "a GARM CI credential directory"
	case rel == ".config/gh/hosts.yml":
		return "the GitHub CLI token store"
	case strings.HasPrefix(rel, ".aws/credentials"):
		return "the AWS credentials file"
	case strings.HasPrefix(rel, ".ssh/id_") && !strings.HasSuffix(base, ".pub"):
		return "an SSH private key"
	case base == "clouds.yaml" || base == "secure.yaml":
		return "an OpenStack credentials file"
	case base == ".github-token" || base == ".gitlab-token" || base == ".netrc":
		return "a token file"
	case strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") ||
		strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".pfx"):
		return "a key or certificate bundle"
	}
	return ""
}

// CheckFile opens path ITSELF and scans what it holds. Only regular files are
// read, at most [MaxBytes] of them. An unreadable file is a Verdict with Err
// set and nothing refused: a refusal is not a verdict about a file never seen.
func CheckFile(path, home string) Verdict {
	v := Verdict{Path: path}
	st, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			v.Err = err
		}
		return v
	}
	if !st.Mode().IsRegular() {
		return v
	}
	data, err := readBounded(path)
	if err != nil {
		v.Err = err
		return v
	}
	if why := SensitivePath(path, home); why != "" && !onlyPublic(data) {
		v.ByPath = why
		return v
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return v // binary: not displayable text
	}
	fs := Scan(data, strings.ToLower(filepath.Ext(path)))
	v.Kinds = Kinds(fs)
	if len(fs) > 0 {
		v.Line = fs[0].Line
	}
	return v
}

// onlyPublic is true for a .pem that holds certificates and public keys only.
func onlyPublic(data []byte) bool {
	s := string(data)
	return strings.Contains(s, "-----BEGIN ") && !strings.Contains(s, "PRIVATE KEY") &&
		!strings.Contains(s, "-----BEGIN PGP") &&
		(strings.Contains(s, "CERTIFICATE-----") || strings.Contains(s, "PUBLIC KEY-----"))
}
