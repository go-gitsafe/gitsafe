package secretarg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⛔ Ce test existe parce que la liste d'installation du README nommait 9 des 15
// commandes. Les absences qui comptaient:
//
//   - `git-credential-tokenfile`, que `gitpush` EXIGE — il nomme `tokenfile` comme
//     pourvoyeur et git le résout en `git-credential-tokenfile` sur le PATH. Sans
//     lui, rien ne sert le jeton et le premier push échoue, ce qui est exactement
//     le moment où quelqu'un met un identifiant dans une URL.
//   - `ghrelease`, à qui le README consacre pourtant une section entière.
//
// Une commande qu'on n'installe pas est une commande qui n'existe pas, et la liste
// se périme en silence à chaque commande ajoutée.
func TestTheInstallListNamesEveryCommand(t *testing.T) {
	root := moduleRoot(t)
	ents, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		t.Skipf("pas de répertoire cmd: %v", err)
	}
	var cmds []string
	for _, e := range ents {
		if e.IsDir() {
			cmds = append(cmds, e.Name())
		}
	}
	if len(cmds) < 5 {
		t.Fatalf("seulement %d commandes vues: la lecture de cmd/ ne rend presque rien", len(cmds))
	}

	b, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Skipf("pas de README: %v", err)
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`cmd/([a-z0-9-]+)@latest`).FindAllStringSubmatch(string(b), -1) {
		listed[m[1]] = true
	}
	// ⭐ L'autre moitié: si le motif ne reconnaît plus une ligne d'installation, il
	// ne trouve rien et le test rapporterait « tout manque » — ou, si on inversait
	// la comparaison, « rien ne manque ». Les deux sont faux pour la même raison.
	if len(listed) == 0 {
		t.Fatal("aucune ligne `go install .../cmd/X@latest` reconnue dans le README: " +
			"le motif ne lit plus la liste, et ce test ne contrôle rien")
	}

	var missing []string
	for _, c := range cmds {
		if !listed[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d commande(s) que la liste d'installation du README ne nomme pas: %s\n  "+
			"Une commande qu'on n'installe pas est une commande qui n'existe pas.",
			len(missing), strings.Join(missing, " "))
	}
	for name := range listed {
		if _, err := os.Stat(filepath.Join(root, "cmd", name)); err != nil {
			t.Errorf("le README fait installer cmd/%s, qui n'existe pas", name)
		}
	}
	t.Logf("%d commandes, %d nommées dans la liste", len(cmds), len(listed))
}
