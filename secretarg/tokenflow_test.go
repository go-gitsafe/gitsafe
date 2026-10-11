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

// ⛔ Ce test répond à la question que le paquet pose, retournée vers NOUS: est-ce que
// l'un de NOS outils met le jeton là où tout le monde le lit?
//
// L'audit du 2026-10-10 a suivi les six commandes qui appellent ghauth.Read et n'a
// rien trouvé: le jeton ne va que vers des aides HTTP.
//
// ⛔⛔ Et ce test ne suivait QUE ghauth.Read. `glauth.Read` est un second lecteur de
// jeton — glscopes et glci s'en servent — donc le garde-fou était posé à UN SEUL des
// deux endroits, ce qui est exactement la faute qu'il existe pour empêcher. Corrigé
// le 2026-10-11, avec un troisième test qui ÉNUMÈRE les lecteurs du dépôt et échoue
// si un quatrième paquet d'authentification apparaît sans être couvert: une liste
// écrite à la main hérite de l'angle mort de celui qui l'a écrite. Mais une lecture n'est pas une
// garantie. ghrunner montre la pression exacte: actions/runner veut le jeton en
// `--token`, et son en-tête explique longuement comment il l'évite. La prochaine
// personne qui en aura besoin atteindra `--token`.
//
// La règle: un identifiant qui tient la valeur rendue par un lecteur de jeton
// (ghauth.Read, glauth.Read) ne doit apparaître dans AUCUN argument de
// exec.Command/CommandContext, ni dans os.Setenv, ni dans une affectation à .Env. Peu importe par combien de Sprintf il passe: on
// cherche l'identifiant n'importe où dans le sous-arbre de l'argument.
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
			t.Errorf("%s: « %s » tient la valeur d'un lecteur de jeton et atteint %s — "+
				"une ligne de commande va dans la liste des processus, et un "+
				"environnement se lit dans /proc et dans tout vidage de plantage",
				rel, v.ident, v.where)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// ⭐ Les deux moitiés. Un balayage qui ne lit rien ne rapporte rien, et passe.
	if files < 20 {
		t.Fatalf("seulement %d fichiers .go lus: le balayage ne voit pas le module", files)
	}
	if withToken < 6 {
		t.Fatalf("seulement %d fichiers tiennent un jeton; il y en avait 7 "+
			"le 2026-10-10. Si le nom a changé, ce test ne contrôle plus rien", withToken)
	}
	t.Logf("%d fichiers .go, %d tiennent la valeur d'un lecteur de jeton", files, withToken)
}

// ⭐ Et l'autre moitié: le détecteur doit RECONNAÎTRE une violation plantée. Sans ce
// témoin, « aucune fuite » ne dit que « je n'ai pas su chercher ».
func TestTheDetectorCatchesAPlantedLeak(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      bool
	}{
		{"argv direct", `package p
func f() { tok, _ := ghauth.Read(p); exec.Command("gh", "api", "-H", tok) }`, true},
		{"argv par Sprintf", `package p
func f() { tok, _ := ghauth.Read(p); exec.Command("sh", "-c", fmt.Sprintf("x %s", tok)) }`, true},
		{"environnement", `package p
func f() { tok, _ := ghauth.Read(p); cmd.Env = append(cmd.Env, "GH_TOKEN="+tok) }`, true},
		{"os.Setenv", `package p
func f() { tok, _ := ghauth.Read(p); os.Setenv("GH_TOKEN", tok) }`, true},
		{"argv, lecteur GitLab", `package p
func f() { tok, _ := glauth.Read(p); exec.Command("glab", "--token", tok) }`, true},
		{"en-tête HTTP, admis", `package p
func f() { tok, _ := ghauth.Read(p); req.Header.Set("Authorization", "Bearer "+tok) }`, false},
		{"redact, admis", `package p
func f() { tok, _ := ghauth.Read(p); red := redact.New(tok); _ = red }`, false},
		{"argv sans jeton, admis", `package p
func f() { exec.Command("git", "remote", "get-url", "origin") }`, false},
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", c.src, 0)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := len(leaksToArgvOrEnv(f, tokenHolders(f))) > 0
		if got != c.want {
			t.Errorf("%s: fuite détectée = %v, attendu %v", c.name, got, c.want)
		}
	}
}

// tokenReaders are the package-level functions that hand a process a credential.
// Both halves of the fleet have one, and the test below asserts there are no others.
var tokenReaders = map[string]string{"ghauth": "Read", "glauth": "Read"}

// tokenHolders rend les identifiants auxquels la valeur d'un lecteur de jeton est
// affectée.
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

// leaksToArgvOrEnv rend les endroits où un identifiant porteur atteint un argv ou un
// environnement. Il regarde tout le sous-arbre de chaque argument, donc un Sprintf
// intermédiaire ne cache rien.
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
				where = "une ligne de commande"
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
						out = append(out, leak{id, "l'environnement d'un sous-processus"})
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
	t.Skip("racine du module introuvable")
	return ""
}

// ⛔⛔ Ce test existe parce que la liste `tokenReaders` est écrite à la main, et
// qu'une liste écrite à la main hérite de l'angle mort de qui l'a écrite: la première
// version ne contenait que `ghauth`, donc le garde-fou ci-dessus ne regardait pas
// `glscopes` ni `glci`, qui lisent un jeton par `glauth.Read`. Le garde-fou était
// posé à UN SEUL des deux endroits — la faute même qu'il sert à empêcher.
//
// Il énumère donc les lecteurs que le dépôt contient réellement et échoue si l'un
// n'est pas couvert. Le critère est la SIGNATURE — `func Read(path string) (string,
// error)` dans un paquet qui n'est pas `main` — et non le nom du paquet: un troisième
// fournisseur ne s'appellera pas forcément `*auth`.
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
		t.Fatalf("seulement %d fichiers lus: le balayage ne voit pas le module", scanned)
	}
	if len(found) == 0 {
		t.Fatal("aucun lecteur de jeton trouvé: la signature recherchée ne correspond plus " +
			"à rien, et ce test ne contrôle donc rien")
	}
	for pkg, where := range found {
		if _, ok := tokenReaders[pkg]; !ok {
			t.Errorf("%s expose un lecteur de jeton (%s) que tokenReaders ne nomme pas: "+
				"le garde-fou ne suit donc pas ce qu'il rend. L'ajouter.", pkg, where)
		}
	}
	for pkg := range tokenReaders {
		if _, ok := found[pkg]; !ok {
			t.Errorf("tokenReaders nomme %s, qui n'expose plus de lecteur de jeton: "+
				"une entrée morte fait croire à une couverture qui n'existe plus", pkg)
		}
	}
	t.Logf("%d fichiers lus, lecteurs trouvés: %v", scanned, found)
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
