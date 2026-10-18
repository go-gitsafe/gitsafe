package secretarg

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ This test asks the question the package asks, turned back on US: does one
// of OUR tools put the token where everybody can read it?
//
// The audit of 2026-10-10 followed the six commands that call ghauth.Read and
// found nothing: the token only ever reaches HTTP helpers.
//
// ⛔⛔ And this test followed ONLY ghauth.Read. `glauth.Read` is a second token
// reader — glscopes and glci use it — so the guard was placed at ONE of the two
// sites, which is exactly the mistake it exists to prevent. Fixed on 2026-10-11,
// with a third test that ENUMERATES the repository's readers and fails if a
// fourth authentication package appears uncovered: a hand-written list inherits
// the blind spot of whoever wrote it. But a reading is not a guarantee. ghrunner
// shows the exact pressure: actions/runner wants the token in `--token`, and its
// own header explains at length how it avoids that. The next person who needs it
// will reach for `--token`.
//
// The rule: an identifier holding the value returned by a token reader
// (ghauth.Read, glauth.Read) must appear in NO argument of
// exec.Command/CommandContext, nor in os.Setenv, nor in an assignment to .Env.
// However many Sprintf calls it passes through makes no difference: the
// identifier is looked for anywhere in the argument's subtree.
func TestNoToolPutsTheTokenOnACommandLineOrInTheEnvironment(t *testing.T) {
	root := moduleRoot(t)
	var files, withToken int
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		files++
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, src, 0)
		if err != nil {
			return err
		}
		holders := tokenHolders(f)
		if len(holders) > 0 {
			withToken++
		}
		for _, v := range leaksToArgvOrEnv(f, holders) {
			rel, _ := filepath.Rel(root, p)
			t.Errorf("%s: %q holds the value of a token reader and reaches %s — "+
				"a command line goes into the process list, and an environment "+
				"is readable in /proc and in every crash dump",
				rel, v.ident, v.where)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// ⭐ Both halves. A scan that reads nothing reports nothing, and passes.
	if files < 20 {
		t.Fatalf("only %d .go file(s) read: the scan cannot see the module", files)
	}
	if withToken < 6 {
		t.Fatalf("only %d file(s) hold a token; there were 7 on 2026-10-10. "+
			"If the name has changed, this test no longer checks anything", withToken)
	}
	t.Logf("%d .go files, %d hold the value of a token reader", files, withToken)
}

// ⭐ And the other half: the detector must RECOGNISE a planted violation.
// Without that witness, "no leak" only says "I did not know how to look".
func TestTheDetectorCatchesAPlantedLeak(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      bool
	}{
		{"straight into argv", `package p
func f() { tok, _ := ghauth.Read(p); exec.Command("gh", "api", "-H", tok) }`, true},
		{"into argv through Sprintf", `package p
func f() { tok, _ := ghauth.Read(p); exec.Command("sh", "-c", fmt.Sprintf("x %s", tok)) }`, true},
		{"into the environment", `package p
func f() { tok, _ := ghauth.Read(p); cmd.Env = append(cmd.Env, "GH_TOKEN="+tok) }`, true},
		{"os.Setenv", `package p
func f() { tok, _ := ghauth.Read(p); os.Setenv("GH_TOKEN", tok) }`, true},
		{"into argv, from the GitLab reader", `package p
func f() { tok, _ := glauth.Read(p); exec.Command("glab", "--token", tok) }`, true},
		{"an HTTP header, allowed", `package p
func f() { tok, _ := ghauth.Read(p); req.Header.Set("Authorization", "Bearer "+tok) }`, false},
		{"redact, allowed", `package p
func f() { tok, _ := ghauth.Read(p); red := redact.New(tok); _ = red }`, false},
		{"argv with no token in it, allowed", `package p
func f() { exec.Command("git", "remote", "get-url", "origin") }`, false},
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", c.src, 0)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := len(leaksToArgvOrEnv(f, tokenHolders(f))) > 0
		if got != c.want {
			t.Errorf("%s: leak detected = %v, want %v", c.name, got, c.want)
		}
	}
}

// tokenReaders are the package-level functions that hand a process a credential.
// Both halves of the fleet have one, and the test below asserts there are no others.
var tokenReaders = map[string]string{"ghauth": "Read", "glauth": "Read"}

// tokenHolders returns the identifiers a token reader's value is assigned to.
func tokenHolders(f *ast.File) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		named := false
		for pkg, fn := range tokenReaders {
			if isSelector(call.Fun, pkg, fn) {
				named = true
				break
			}
		}
		if !named {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
			out[id.Name] = true
		}
		return true
	})
	return out
}

type leak struct{ ident, where string }

// leaksToArgvOrEnv returns the places where a holding identifier reaches an argv
// or an environment. It looks at each argument's whole subtree, so an
// intervening Sprintf hides nothing.
func leaksToArgvOrEnv(f *ast.File, holders map[string]bool) []leak {
	if len(holders) == 0 {
		return nil
	}
	var out []leak
	mentions := func(e ast.Expr) string {
		found := ""
		ast.Inspect(e, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && holders[id.Name] && found == "" {
				found = id.Name
			}
			return true
		})
		return found
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			var where string
			switch {
			case isSelector(x.Fun, "exec", "Command"), isSelector(x.Fun, "exec", "CommandContext"):
				where = "a command line"
			case isSelector(x.Fun, "os", "Setenv"):
				where = "os.Setenv"
			default:
				return true
			}
			for _, a := range x.Args {
				if id := mentions(a); id != "" {
					out = append(out, leak{id, where})
				}
			}
		case *ast.AssignStmt:
			for _, l := range x.Lhs {
				sel, ok := l.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Env" {
					continue
				}
				for _, r := range x.Rhs {
					if id := mentions(r); id != "" {
						out = append(out, leak{id, "a subprocess's environment"})
					}
				}
			}
		}
		return true
	})
	return out
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		d = filepath.Dir(d)
	}
	t.Skip("module root not found")
	return ""
}

// ⛔⛔ This test exists because the `tokenReaders` list is written by hand, and a
// hand-written list inherits the blind spot of whoever wrote it: the first
// version held only `ghauth`, so the guard above was not looking at `glscopes`
// or `glci`, which read a token through `glauth.Read`. The guard was placed at
// ONE of the two sites — the very mistake it serves to prevent.
//
// So it enumerates the readers the repository actually contains and fails if one
// is uncovered. The criterion is the SIGNATURE — `func Read(path string)
// (string, error)` in a package that is not `main` — and not the package name: a
// third provider will not necessarily be called `*auth`.
func TestEveryTokenReaderInTheRepositoryIsCovered(t *testing.T) {
	root := moduleRoot(t)
	found := map[string]string{}
	scanned := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		scanned++
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			return err
		}
		if f.Name.Name == "main" {
			return nil
		}
		for _, dec := range f.Decls {
			fn, ok := dec.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !readsACredential(fn) {
				continue
			}
			rel, _ := filepath.Rel(root, p)
			found[f.Name.Name] = rel
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 20 {
		t.Fatalf("only %d file(s) read: the scan cannot see the module", scanned)
	}
	if len(found) == 0 {
		t.Fatal("no token reader found: the signature being looked for no longer " +
			"matches anything, so this test checks nothing")
	}
	for pkg, where := range found {
		if _, ok := tokenReaders[pkg]; !ok {
			t.Errorf("%s exposes a token reader (%s) that tokenReaders does not name, "+
				"so the guard does not follow what it returns. Add it.", pkg, where)
		}
	}
	for pkg := range tokenReaders {
		if _, ok := found[pkg]; !ok {
			t.Errorf("tokenReaders names %s, which no longer exposes a token reader: "+
				"a dead entry suggests a coverage that no longer exists", pkg)
		}
	}
	t.Logf("%d file(s) read, readers found: %v", scanned, found)
}

// readsACredential matches `func Read(path string) (string, error)`.
func readsACredential(fn *ast.FuncDecl) bool {
	if fn.Name.Name != "Read" || fn.Type.Params == nil || fn.Type.Results == nil {
		return false
	}
	if len(fn.Type.Params.List) != 1 || len(fn.Type.Results.List) != 2 {
		return false
	}
	return typeName(fn.Type.Params.List[0].Type) == "string" &&
		typeName(fn.Type.Results.List[0].Type) == "string" &&
		typeName(fn.Type.Results.List[1].Type) == "error"
}

func typeName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
