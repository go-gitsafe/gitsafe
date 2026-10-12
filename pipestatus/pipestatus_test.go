package pipestatus

import "testing"

// ⛔ Les trois premiers cas sont les formes RÉELLEMENT tapées le 2026-10-11, dans
// la même après-midi, par quelqu'un qui avait relu sa propre note entre deux.
func TestItRefusesAStatusThatBelongsToTheLastStage(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"après go build | tail (tel quel)",
			`go build -o /dev/null . | tail -5 ; echo "statut=$?"`},
		{"après go build | head, sur deux lignes",
			"GOWORK=off go build ./cmd/reauthor/ 2>&1 | head -20 | sed 's/^/  /'\necho \"  build statut=$?\""},
		{"après un balayage | tail",
			"reauthor -scan ~/src 2>&1 | tail -40\necho \"statut=$?\""},
		{"affecté à une variable", `a | b; code=$?`},
		{"rendu en sortie", `a | b; exit $?`},
		{"éprouvé dans un test", `a | b; if [ $? -ne 0 ]; then echo non; fi`},
		{"forme accoladée", `a | b; echo ${?}`},
		{"le tuyau est deux statements plus haut, séparés par du vide",
			"a | b;\n\necho $?"},
	} {
		f := Check(c.cmd)
		if !f.Found() {
			t.Errorf("%s: NON refusé: %q", c.name, c.cmd)
			continue
		}
		if f.String() == "" {
			t.Errorf("%s: refus sans explication", c.name)
		}
	}
}

// ⭐ La moitié qui empêche le contournement. Un garde-fou qui refuse du travail
// ordinaire est un garde-fou qu'on apprend à éviter, et il ne protège alors plus
// rien — ce dépôt a déjà vu cela arriver à une règle qui filtrait « push » trop
// largement.
func TestItAllowsWhatIsNotTheMistake(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"aucun tuyau: le statut est bien celui de la commande", `go build ./...; echo $?`},
		{"pipefail: le statut EST celui du tuyau", `set -o pipefail; a | b; echo $?`},
		{"pipefail avec d'autres drapeaux", `set -eo pipefail
a | b
echo $?`},
		{"personne ne lit de statut", `go build ./... | tail -5`},
		{"le statut est EMPLOYÉ, pas rapporté", `a | b && echo ok`},
		{"|| n'est pas un tuyau", `a || b; echo $?`},
		// ⭐ Un tuyau PLUS HAUT dans la chaîne n'empoisonne pas un statement
		// ultérieur: ici $? est le statut de `c`, qui n'est pas un tuyau, donc
		// la lecture est juste. (Ce cas était du mauvais côté de la table: le
		// code avait raison et le test avait tort.)
		{"un tuyau plus haut, mais le statut est celui de c", `a | b && c; echo $?`},
		{"|| suivi d'une lecture", `make || true; echo $?`},
		{"en parler entre apostrophes", `echo 'ne pas écrire a | b puis $?'`},
		{"en parler dans un heredoc cité", `cat > note.md <<'EOF'
a | b ; echo "statut=$?"
EOF`},
		{"un tuyau dans une chaîne, pas un tuyau", `grep "a|b" f.txt; echo $?`},
		{"commande vide", ``},
		{"$? seul, sans rien avant", `echo $?`},
	} {
		if f := Check(c.cmd); f.Found() {
			t.Errorf("%s: refusé à tort (%s): %q", c.name, f, c.cmd)
		}
	}
}

// ⛔ Les guillemets DOUBLES développent, donc `echo "statut=$?"` est la faute
// elle-même — et c'est précisément l'orthographe tapée trois fois.
func TestDoubleQuotesAreNotAnExcuse(t *testing.T) {
	if f := Check(`a | b; echo "statut=$?"`); !f.Found() {
		t.Error(`"statut=$?" entre guillemets doubles développe: ce n'est pas en parler, c'est le faire`)
	}
	if f := Check(`a | b; echo 'statut=$?'`); f.Found() {
		t.Errorf("entre apostrophes le shell ne développe pas: %s", f)
	}
}

// Le refus doit NOMMER les deux statements, pour pointer au lieu d'affirmer.
func TestTheRefusalNamesBothSides(t *testing.T) {
	f := Check(`go build ./... | tail -5; echo "statut=$?"`)
	if !f.Found() {
		t.Fatal("non refusé")
	}
	if f.Pipeline == "" || f.Statement == "" {
		t.Errorf("le refus ne nomme pas les deux côtés: %+v", f)
	}
	for _, want := range []string{"tail -5", "LAST stage"} {
		if !contains(f.String(), want) {
			t.Errorf("l'explication ne dit pas %q: %s", want, f)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
