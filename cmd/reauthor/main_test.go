package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ Les identités de ces tests sont des adresses en `.invalid`, le TLD que la
// RFC 2606 réserve pour cela. Un test qui porterait une adresse réelle ferait
// exactement ce que cet outil existe pour défaire.
const (
	mineEmail  = "someone@old.invalid"
	mineName   = "Some One"
	otherEmail = "yannick@elsewhere.invalid"
	otherName  = "Yannick Fitamant"
	wantName   = "publicname"
	wantEmail  = "publicname@users.noreply.github.invalid"
)

func hermetic(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

func run2(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoWith builds a repository: a commit of yours, one of somebody else's, a
// merge, a lightweight tag and an annotated tag whose message has a BODY.
func repoWith(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run2(t, dir, nil, "init", "-q", "-b", "main", ".")
	run2(t, dir, nil, "config", "user.name", wantName)
	run2(t, dir, nil, "config", "user.email", wantEmail)

	as := func(n, e, when string) []string {
		return []string{
			"GIT_AUTHOR_NAME=" + n, "GIT_AUTHOR_EMAIL=" + e, "GIT_AUTHOR_DATE=" + when,
			"GIT_COMMITTER_NAME=" + n, "GIT_COMMITTER_EMAIL=" + e, "GIT_COMMITTER_DATE=" + when,
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		run2(t, dir, nil, "add", name)
	}

	write("a.txt", "a\n")
	run2(t, dir, as(mineName, mineEmail, "2025-01-02T03:04:05+0200"), "commit", "-q", "-m", "mine: root")

	run2(t, dir, nil, "checkout", "-q", "-b", "side")
	write("b.txt", "b\n")
	run2(t, dir, as(otherName, otherEmail, "2025-02-03T04:05:06+0100"), "commit", "-q", "-m", "theirs: on a branch")

	run2(t, dir, nil, "checkout", "-q", "main")
	write("c.txt", "c\n")
	run2(t, dir, as(mineName, mineEmail, "2025-03-04T05:06:07+0300"), "commit", "-q", "-m", "mine: second")
	run2(t, dir, as(mineName, mineEmail, "2025-04-05T06:07:08+0000"),
		"merge", "-q", "--no-ff", "side", "-m", "mine: the merge")

	run2(t, dir, nil, "tag", "v0.0.1-light")
	run2(t, dir, as(mineName, mineEmail, "2025-05-06T07:08:09+0200"),
		"tag", "-a", "v0.1.0", "-m", "v0.1.0\n\nA body that must survive.\nTwo lines of it.")
	return dir
}

func identitiesFile(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "identities")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func exec2(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestYoursIsRewrittenAndNobodyElsesIs(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)

	before := run2(t, dir, nil, "log", "--branches", "--tags", "--format=%an <%ae>%x09%aI%x09%cI%x09%s")
	code, out, errb := exec2(t, "-identities", ids, dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	after := run2(t, dir, nil, "log", "--branches", "--tags", "--format=%an <%ae>%x09%aI%x09%cI%x09%s")

	// ⭐ Les DATES doivent être identiques ligne pour ligne, et seules les
	// identités doivent avoir bougé.
	bl, al := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(bl) != len(al) {
		t.Fatalf("%d commits avant, %d après", len(bl), len(al))
	}
	for i := range bl {
		bf, af := strings.Split(bl[i], "\t"), strings.Split(al[i], "\t")
		if bf[1] != af[1] || bf[2] != af[2] {
			t.Errorf("dates changées sur %q: %v -> %v", bf[3], bf[1:3], af[1:3])
		}
		if bf[3] != af[3] {
			t.Errorf("message changé: %q -> %q", bf[3], af[3])
		}
		isMine := strings.Contains(bf[0], mineEmail)
		wantWho := bf[0]
		if isMine {
			wantWho = wantName + " <" + wantEmail + ">"
		}
		if af[0] != wantWho {
			t.Errorf("sur %q: auteur = %q, attendu %q", bf[3], af[0], wantWho)
		}
	}

	// L'autre contributeur doit être intact, nom ET adresse.
	if !strings.Contains(after, otherName+" <"+otherEmail+">") {
		t.Errorf("le commit d'un tiers a été touché:\n%s", after)
	}
	if strings.Contains(after, mineEmail) {
		t.Errorf("l'ancienne adresse survit:\n%s", after)
	}
}

// ⛔ Le défaut que le script shell portait: `git tag -a -m "$subject"` ne garde
// que la première ligne, donc tout tag annoté avec un corps le perdait.
func TestAnAnnotatedTagKeepsItsWholeMessageAndMovesWithIt(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	wasHead := run2(t, dir, nil, "rev-parse", "main")

	if code, out, errb := exec2(t, "-identities", ids, dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}

	body := run2(t, dir, nil, "for-each-ref", "--format=%(contents)", "refs/tags/v0.1.0")
	for _, want := range []string{"A body that must survive.", "Two lines of it."} {
		if !strings.Contains(body, want) {
			t.Errorf("le message du tag annoté a perdu %q; il dit:\n%s", want, body)
		}
	}
	// ⛔ Et il doit pointer sur le commit RECONSTRUIT, pas sur l'ancien.
	if onto := run2(t, dir, nil, "rev-parse", "v0.1.0^{commit}"); onto == wasHead {
		t.Error("le tag annoté pointe encore sur l'ancien commit")
	}
	if tagger := run2(t, dir, nil, "for-each-ref", "--format=%(taggeremail)", "refs/tags/v0.1.0"); strings.Contains(tagger, mineEmail) {
		t.Errorf("le tagger porte encore l'ancienne adresse: %s", tagger)
	}
	// Le tag léger doit avoir suivi aussi.
	if run2(t, dir, nil, "rev-parse", "v0.0.1-light") == wasHead {
		t.Error("le tag léger pointe encore sur l'ancien commit")
	}
}

func TestEveryTreeIsUnchangedAndTheMergeKeepsItsShape(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	treeBefore := run2(t, dir, nil, "rev-parse", "main^{tree}")
	parentsBefore := run2(t, dir, nil, "rev-list", "--parents", "-n1", "main")

	if code, out, errb := exec2(t, "-identities", ids, dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if got := run2(t, dir, nil, "rev-parse", "main^{tree}"); got != treeBefore {
		t.Errorf("l'arbre de main a changé: %s -> %s", treeBefore, got)
	}
	if got := len(strings.Fields(run2(t, dir, nil, "rev-list", "--parents", "-n1", "main"))); got != len(strings.Fields(parentsBefore)) {
		t.Errorf("la fusion a perdu un parent: %d champs contre %d", got, len(strings.Fields(parentsBefore)))
	}
}

// Un nom est à vous quelle que soit l'adresse — c'est le cas où l'adresse d'un
// inconnu porte votre nom. Et réciproquement.
func TestANameMatchesWhateverTheAddressIs(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "name: "+mineName)
	if code, out, errb := exec2(t, "-identities", ids, dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	after := run2(t, dir, nil, "log", "--branches", "--tags", "--format=%an <%ae>")
	if strings.Contains(after, mineName) {
		t.Errorf("l'ancien NOM survit:\n%s", after)
	}
	if !strings.Contains(after, otherName) {
		t.Errorf("le tiers a disparu:\n%s", after)
	}
}

func TestRefusals(t *testing.T) {
	hermetic(t)
	t.Run("sans -identities", func(t *testing.T) {
		if code, _, errb := exec2(t, t.TempDir()); code != 2 || !strings.Contains(errb, "-identities is required") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
	t.Run("fichier d'identités vide", func(t *testing.T) {
		ids := identitiesFile(t, "# rien que des commentaires")
		if code, _, errb := exec2(t, "-identities", ids, t.TempDir()); code != 1 ||
			!strings.Contains(errb, "names no identity") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
	t.Run("ligne mal formée", func(t *testing.T) {
		ids := identitiesFile(t, "someone@old.invalid")
		if code, _, errb := exec2(t, "-identities", ids, t.TempDir()); code != 1 ||
			!strings.Contains(errb, "expected") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
	t.Run("arbre sale", func(t *testing.T) {
		dir := repoWith(t)
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changé\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ids := identitiesFile(t, "email: "+mineEmail)
		if code, _, errb := exec2(t, "-identities", ids, dir); code != 1 ||
			!strings.Contains(errb, "uncommitted changes to tracked files") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
	t.Run("un fichier NON suivi ne suffit pas à refuser", func(t *testing.T) {
		dir := repoWith(t)
		if err := os.WriteFile(filepath.Join(dir, "build.log"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ids := identitiesFile(t, "email: "+mineEmail)
		if code, out, errb := exec2(t, "-identities", ids, dir); code != 0 {
			t.Errorf("un fichier non suivi a fait refuser: exit %d\n%s%s", code, out, errb)
		}
	})
	t.Run("sans branche ni tag", func(t *testing.T) {
		dir := t.TempDir()
		run2(t, dir, nil, "init", "-q", "-b", "main", ".")
		// ⛔ Il faut une identité configurée, sinon le refus vient de son absence
		// et ce test affirmerait le mauvais refus.
		run2(t, dir, nil, "config", "user.name", wantName)
		run2(t, dir, nil, "config", "user.email", wantEmail)
		ids := identitiesFile(t, "email: "+mineEmail)
		if code, _, errb := exec2(t, "-identities", ids, dir); code != 1 ||
			!strings.Contains(errb, "nothing would be published") {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
}

// ⛔ Tuer le shell qui a lancé une réécriture ne tue pas la réécriture: en août
// 2026 deux d'entre elles tournaient sur le même dépôt, toutes deux en route
// vers update-ref et un reset --hard sur les mêmes refs.
func TestASecondRewriteIsRefusedWhileOneIsRunning(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	gitdir := run2(t, dir, nil, "rev-parse", "--absolute-git-dir")
	if err := os.Mkdir(filepath.Join(gitdir, "reauthor.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	ids := identitiesFile(t, "email: "+mineEmail)
	code, _, errb := exec2(t, "-identities", ids, dir)
	if code != 1 || !strings.Contains(errb, "another rewrite is running") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
	if !strings.Contains(errb, "rmdir") {
		t.Errorf("le refus ne dit pas comment lever un verrou orphelin: %q", errb)
	}
}

func TestADryRunWritesNothing(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	before := run2(t, dir, nil, "rev-parse", "main")
	code, out, errb := exec2(t, "-identities", ids, "-dry-run", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if got := run2(t, dir, nil, "rev-parse", "main"); got != before {
		t.Errorf("un essai à blanc a réécrit main: %s -> %s", before, got)
	}
	for _, want := range []string{"4 commits: 3 yours, 1 left alone", "nothing was written"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout ne dit pas %q:\n%s", want, out)
		}
	}
}

func TestToOverridesTheConfiguredIdentity(t *testing.T) {
	hermetic(t)
	dir := repoWith(t)
	ids := identitiesFile(t, "email: "+mineEmail)
	if code, out, errb := exec2(t, "-identities", ids, "-to", "Autre <autre@example.invalid>", dir); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if after := run2(t, dir, nil, "log", "--branches", "--format=%an <%ae>"); !strings.Contains(after, "autre@example.invalid") {
		t.Errorf("-to n'a pas été employé:\n%s", after)
	}
	t.Run("forme refusée", func(t *testing.T) {
		if code, _, errb := exec2(t, "-identities", ids, "-to", "pas une identité", dir); code != 1 ||
			!strings.Contains(errb, `-to must be`) {
			t.Errorf("exit %d, stderr %q", code, errb)
		}
	})
}
