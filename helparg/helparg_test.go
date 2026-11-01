package helparg

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestIsKnowsAllFourSpellings(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help", "help"} {
		if !Is(arg) {
			t.Errorf("Is(%q) = false, want true", arg)
		}
	}
	for _, arg := range []string{"", "h", "-H", "--Help", "helpme", "-help-me", "workflow", "go-helpers/help"} {
		if Is(arg) {
			t.Errorf("Is(%q) = true, want false", arg)
		}
	}
}

// ⛔⛔ THE WHOLE POINT OF THIS PACKAGE IS THAT IT COSTS NOTHING TO IMPORT, so
// that is what is asserted — in the source, not in a comment. 365 KB of cobra
// and pflag reached `gitpush` because these six lines sat in a package that
// imports them, and nothing said so.
//
// An empty import list is the invariant. If this package ever needs one, the
// commands that depend on it are the ones to think about first: `gitpush`,
// which exists so a token never reaches a command line.
func TestThisPackageImportsNothing(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		t.Fatal("parsed no package at all: this test cannot see what it is for")
	}
	var checked int
	for name, pkg := range pkgs {
		if name != "helparg" {
			continue
		}
		for path, file := range pkg.Files {
			// ⛔ The test file is in this same package, so it is in this map:
			// the first version of this test reported its own `testing` and
			// `go/parser` imports. What ships is what matters.
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			checked++
			for _, imp := range file.Imports {
				t.Errorf("%s imports %s: this package must import nothing, "+
					"because gitpush links whatever it does", path, imp.Path.Value)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no non-test file in package helparg: nothing was checked")
	}
}
