package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cleanRepo is a checkout with one commit under the public identity only.
func cleanRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	run2(t, dir, nil, "config", "user.name", wantName)
	run2(t, dir, nil, "config", "user.email", wantEmail)
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "add", "x.txt")
	run2(t, dir, nil, "commit", "-q", "-m", "clean")
	return dir
}

func TestAScanNamesOnlyTheCheckoutsThatCarryYou(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dirty := repoWith(t) // 3 commits of "mine", 1 of somebody else's
	// Put a copy under the root so the walk finds it.
	nested := filepath.Join(root, "nested", "dirty")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, t.TempDir(), nil, "clone", "-q", "--no-local", dirty, nested)
	cleanRepo(t, filepath.Join(root, "tidy"))

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 {
		t.Errorf("un balayage qui TROUVE doit sortir non-zéro, a rendu %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "nested/dirty") {
		t.Errorf("le dépôt porteur n'est pas nommé:\n%s", out)
	}
	if strings.Contains(out, filepath.Join(root, "tidy")) {
		t.Errorf("un dépôt propre a été rapporté:\n%s", out)
	}
	if !strings.Contains(out, "2 checkouts") {
		t.Errorf("le compte de dépôts est faux:\n%s", out)
	}
	if !strings.Contains(out, "1 carry an identity of yours") {
		t.Errorf("le compte de porteurs est faux:\n%s", out)
	}
	// ⭐ L'adresse de l'autre contributeur ne doit PAS être comptée.
	if strings.Contains(out, otherEmail) {
		t.Errorf("l'identité d'un tiers a été comptée:\n%s", out)
	}
}

func TestAScanOfCleanCheckoutsExitsZero(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	cleanRepo(t, filepath.Join(root, "a"))
	cleanRepo(t, filepath.Join(root, "b"))
	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 0 {
		t.Errorf("exit %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "nothing of yours is published") {
		t.Errorf("stdout = %q", out)
	}
}

// ⛔ Le committer compte autant que l'auteur: un commit écrit par quelqu'un
// d'autre et rebasé par vous porte VOTRE adresse comme committer, et elle est
// publiée tout aussi visiblement.
func TestTheCommitterCountsToo(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := filepath.Join(root, "r")
	cleanRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "y.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "add", "y.txt")
	run2(t, dir, []string{
		"GIT_AUTHOR_NAME=" + otherName, "GIT_AUTHOR_EMAIL=" + otherEmail,
		"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
	}, "commit", "-q", "-m", "written by them, committed by me")

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, _ := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 || !strings.Contains(out, mineEmail) {
		t.Errorf("une adresse de committer n'a pas été vue: exit %d\n%s", code, out)
	}
}

func TestAScanRefusesWithoutRoots(t *testing.T) {
	hermetic(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	code, _, errb := exec2(t, "-identities", ids, "-scan")
	if code != 2 || !strings.Contains(errb, "needs the roots spelled out") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
}

// ⭐ Un balayage n'a besoin ni d'identité configurée ni d'écriture, donc il ne
// doit PAS refuser faute de user.name — sinon la moitié détection serait
// inutilisable là où elle sert le plus.
func TestAScanWorksWithNoConfiguredIdentity(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := filepath.Join(root, "r")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	ids := identitiesFile(t, "email: "+mineEmail)
	if code, out, errb := exec2(t, "-identities", ids, "-scan", root); code != 0 {
		t.Errorf("exit %d (une identité configurée ne doit pas être requise)\n%s%s", code, out, errb)
	}
}

// ⛔ Un dépôt vide n'est pas un dépôt illisible: git refuse `log` avec un statut
// non nul, et prendre cela pour une panne de lecture rendrait tout balayage
// « incomplet ».
func TestAnEmptyRepositoryIsNotUnreadable(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := filepath.Join(root, "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 0 {
		t.Errorf("exit %d: un dépôt vide a été pris pour illisible\n%s%s", code, out, errb)
	}
	if strings.Contains(errb, "did not cover everything") {
		t.Errorf("stderr annonce un balayage incomplet: %q", errb)
	}
}
