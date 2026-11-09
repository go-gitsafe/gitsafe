// Package bigblob finds the large files a push would publish, in EVERY commit
// it sends, not only in the tip.
//
// A build binary committed by `git add -A` and removed by the next commit is
// gone from the tree and still in the history: whoever clones gets it, and a
// pull request's head keeps it on the server for good (GitHub's refs/pull/*
// cannot be deleted by the owner). A check on the final tree (CI's "nothing
// big is tracked") cannot see it; the objects a push sends can.
//
// The pure half lives here: parsing what `git rev-list --objects` and
// `git cat-file --batch-check` print. The hook runs git.
package bigblob

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Limit is the size from which a blob is refused: 1 MiB, the bound the
// fleet's CI already applies to tracked files.
const Limit = 1 << 20

// Blob is one large file the push would publish.
type Blob struct {
	SHA  string
	Path string // the first path rev-list named it under; "" if none
	Size int64
}

// String is the line a refusal prints.
func (b Blob) String() string {
	p := b.Path
	if p == "" {
		p = "(no path)"
	}
	return fmt.Sprintf("%s  %s  (blob %s)", Human(b.Size), p, short(b.SHA))
}

// Objects parses `git rev-list --objects` output: one "sha[ path]" per line.
// It returns the object ids in order and the first path seen for each.
func Objects(r io.Reader) (shas []string, path map[string]string) {
	path = map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		sha, p, _ := strings.Cut(line, " ")
		if !isSHA(sha) {
			continue
		}
		if _, seen := path[sha]; !seen {
			shas = append(shas, sha)
			path[sha] = p
		}
	}
	return shas, path
}

// Over parses `git cat-file --batch-check='%(objecttype) %(objectname)
// %(objectsize)'` output and returns the blobs of at least limit bytes, with
// the paths from [Objects], in input order.
func Over(r io.Reader, path map[string]string, limit int64) []Blob {
	var out []Blob
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 || f[0] != "blob" || !isSHA(f[1]) {
			continue
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || size < limit {
			continue
		}
		out = append(out, Blob{SHA: f[1], Path: path[f[1]], Size: size})
	}
	return out
}

// Human prints a size in KiB or MiB with one decimal.
func Human(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
}

// isSHA accepts a SHA-1 or SHA-256 object id in lower-case hex.
func isSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
