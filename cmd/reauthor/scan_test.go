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
	// La synthèse dit REPOSITORIES depuis le repliement des worktrees: un
	// worktree n'est pas une publication distincte.
	if !strings.Contains(out, "1 checkout(s) carry") {
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

// ⛔ La première lecture du balayage de flotte a été « 350 dépôts portent une
// identité » — alors que 215 étaient sur le GitLab d'une institution, où son
// adresse est à sa place, et 71 n'avaient aucun remote. Le total seul est le
// mauvais chiffre pour agir, et j'ai fait la décomposition à la main, hors de
// l'outil. Elle est donc dans l'outil.
func TestTheSummaryGroupsByWhereOriginPoints(t *testing.T) {
	hermetic(t)
	root := t.TempDir()

	// Trois porteurs: un publié sur un hôte public, un sur une forge interne,
	// un sans aucun remote.
	mk := func(name, remote string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "init", "-q", "-b", "main", ".")
		run2(t, dir, nil, "config", "user.name", wantName)
		run2(t, dir, nil, "config", "user.email", wantEmail)
		if remote != "" {
			run2(t, dir, nil, "config", "remote.origin.url", remote)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "add", "f.txt")
		run2(t, dir, []string{
			"GIT_AUTHOR_NAME=" + mineName, "GIT_AUTHOR_EMAIL=" + mineEmail,
			"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
		}, "commit", "-q", "-m", "mine")
	}
	mk("public", "https://github.invalid/o/r.git")
	// ⭐ La forme scp n'a pas de schéma: c'est credurl qui sait la lire, et c'est
	// la raison de ne pas écrire un second parseur d'URL ici.
	mk("internal", "git@gitlab.internal.invalid:group/proj.git")
	mk("nowhere", "")

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	for _, want := range []string{
		"by UPSTREAM, grouped by host",
		"github.invalid",
		"gitlab.internal.invalid",
		"(no remote — published nowhere)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("la synthèse ne dit pas %q:\n%s", want, out)
		}
	}
	// Chaque groupe tient exactement un dépôt et un commit.
	if n := strings.Count(out, "commit(s) in    1 repo(s)"); n != 3 {
		t.Errorf("3 groupes d'un dépôt attendus, %d trouvés:\n%s", n, out)
	}
	// ⛔ Et l'hôte doit figurer sur la ligne de CHAQUE dépôt: un tableau qui
	// compte 88 dépôts sur un hôte public sans les nommer n'est pas
	// exploitable, et le lecteur regroupe alors la sortie par CHEMIN pour les
	// retrouver — la faute même que ce groupement existe pour éviter.
	// ⛔ L'AMONT sur la ligne, pas seulement son hôte: la synthèse compte des
	// amonts, et ne nommer que l'hôte laisse le lecteur chercher lesquels.
	for _, want := range []string{
		filepath.Join(root, "public") + "\n    github.invalid/o/r",
		filepath.Join(root, "nowhere") + "\n    (no remote",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("la ligne du dépôt ne nomme pas son hôte (%q):\n%s", want, out)
		}
	}
}

// Un chemin local comme remote EST un remote, et ce n'est pas « aucun remote ».
func TestALocalPathRemoteIsNotNoRemote(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	dir := filepath.Join(root, "cloned")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	run2(t, dir, nil, "config", "user.name", wantName)
	run2(t, dir, nil, "config", "user.email", wantEmail)
	run2(t, dir, nil, "config", "remote.origin.url", "/some/local/path.git")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run2(t, dir, nil, "add", "f.txt")
	run2(t, dir, []string{
		"GIT_AUTHOR_NAME=" + mineName, "GIT_AUTHOR_EMAIL=" + mineEmail,
		"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
	}, "commit", "-q", "-m", "mine")

	ids := identitiesFile(t, "email: "+mineEmail)
	_, out, _ := exec2(t, "-identities", ids, "-scan", root)
	if !strings.Contains(out, "(a local path)") {
		t.Errorf("un remote qui est un chemin local n'est pas nommé comme tel:\n%s", out)
	}
	if strings.Contains(out, "no remote") {
		t.Errorf("un chemin local a été pris pour une absence de remote:\n%s", out)
	}
}

// ⛔⛔ Le défaut qui a fait bouger le chiffre une troisième fois. Le premier
// balayage groupé annonçait « 89 dépôts GitHub porteurs, 13323 commits » — dont
// une quatre-vingtaine étaient des worktrees d'agents d'UN SEUL fork, chacun
// comptant la même histoire. Un worktree n'est pas une publication distincte.
func TestAWorktreeIsFoldedIntoItsRepository(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	main := filepath.Join(root, "repo")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run2(t, main, nil, "init", "-q", "-b", "main", ".")
	run2(t, main, nil, "config", "user.name", wantName)
	run2(t, main, nil, "config", "user.email", wantEmail)
	run2(t, main, nil, "config", "remote.origin.url", "https://github.invalid/o/r.git")
	if err := os.WriteFile(filepath.Join(main, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run2(t, main, nil, "add", "f.txt")
	run2(t, main, []string{
		"GIT_AUTHOR_NAME=" + mineName, "GIT_AUTHOR_EMAIL=" + mineEmail,
		"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
	}, "commit", "-q", "-m", "mine")
	// Deux worktrees, sous le dépôt lui-même, comme les worktrees d'agents.
	run2(t, main, nil, "worktree", "add", "-q", "-b", "wt1", filepath.Join(main, "wt", "one"))
	run2(t, main, nil, "worktree", "add", "-q", "-b", "wt2", filepath.Join(main, "wt", "two"))

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "1 checkout(s) carry") || !strings.Contains(out, "1 distinct upstream(s)") {
		t.Errorf("le dépôt et ses 2 worktrees ne sont pas repliés en UN:\n%s", out)
	}
	if !strings.Contains(out, "2 of the carriers were worktrees") {
		t.Errorf("le repliement n'est pas dit:\n%s", out)
	}
	// ⭐ Et c'est le dépôt LUI-MÊME qui est nommé, pas un de ses worktrees: c'est
	// le répertoire sur lequel on agit.
	if !strings.Contains(out, main+"\n") {
		t.Errorf("le dépôt principal n'est pas celui qui est nommé:\n%s", out)
	}
	// Le tableau par hôte compte UN dépôt, pas trois.
	if !strings.Contains(out, "in    1 repo(s)   github.invalid") {
		t.Errorf("le tableau par hôte compte les worktrees:\n%s", out)
	}
}

// ⛔⛔ Le quatrième déplacement du chiffre, et la même cause: l'unité comptée.
// Trois clones d'un même projet étaient comptés trois fois, et douze clones
// bac-à-sable d'un fork douze fois. Ce qu'on corrige est l'AMONT, une seule fois.
//
// ⭐ Et les deux orthographes doivent répondre pareil: `git@host:o/r.git` et
// `https://host/o/r` sont le même dépôt.
func TestSeveralClonesOfOneUpstreamCountOnce(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	mk := func(name, remote string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "init", "-q", "-b", "main", ".")
		run2(t, dir, nil, "config", "user.name", wantName)
		run2(t, dir, nil, "config", "user.email", wantEmail)
		run2(t, dir, nil, "config", "remote.origin.url", remote)
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "add", "f.txt")
		run2(t, dir, []string{
			"GIT_AUTHOR_NAME=" + mineName, "GIT_AUTHOR_EMAIL=" + mineEmail,
			"GIT_COMMITTER_NAME=" + mineName, "GIT_COMMITTER_EMAIL=" + mineEmail,
		}, "commit", "-q", "-m", "mine")
	}
	mk("clone-ssh", "git@github.invalid:o/r.git")
	mk("clone-https", "https://github.invalid/o/r")
	mk("elsewhere", "https://github.invalid/o/other.git")

	ids := identitiesFile(t, "email: "+mineEmail)
	code, out, errb := exec2(t, "-identities", ids, "-scan", root)
	if code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "3 checkout(s) carry") {
		t.Errorf("les 3 checkouts doivent être listés, aucun n'est caché:\n%s", out)
	}
	if !strings.Contains(out, "2 distinct upstream(s) to fix") {
		t.Errorf("deux orthographes d'un même amont n'ont pas été repliées:\n%s", out)
	}
	if !strings.Contains(out, "1 of the carriers are further clones") {
		t.Errorf("le clone replié n'est pas dit:\n%s", out)
	}
	if !strings.Contains(out, "in    2 repo(s)   github.invalid") {
		t.Errorf("le tableau par hôte doit compter 2 amonts:\n%s", out)
	}
}
