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
// rien trouvé: le jeton ne va que vers des aides HTTP. Mais une lecture n'est pas une
// garantie. ghrunner montre la pression exacte: actions/runner veut le jeton en
// `--token`, et son en-tête explique longuement comment il l'évite. La prochaine
// personne qui en aura besoin atteindra `--token`.
//
// La règle: un identifiant qui tient la valeur rendue par ghauth.Read ne doit
// apparaître dans AUCUN argument de exec.Command/CommandContext, ni dans os.Setenv,
// ni dans une affectation à .Env. Peu importe par combien de Sprintf il passe: on
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
			t.Errorf("%s: « %s » tient la valeur de ghauth.Read et atteint %s — "+
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
	if withToken < 4 {
		t.Fatalf("seulement %d fichiers tiennent un jeton de ghauth.Read; il y en avait 5 "+
			"le 2026-10-10. Si le nom a changé, ce test ne contrôle plus rien", withToken)
	}
	t.Logf("%d fichiers .go, %d tiennent la valeur de ghauth.Read", files, withToken)
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

// tokenHolders rend les identifiants auxquels la valeur de ghauth.Read est affectée.
func tokenHolders(f *ast.File) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "ghauth", "Read") {
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
