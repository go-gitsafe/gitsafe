package literaltick

import (
	"errors"
	"reflect"
	"testing"
)

// The cases zsh found (#121). Each is a command in which zsh runs the backtick
// and bash does not (a ( after an argument is a syntax error there, a glob group
// in zsh). The rule must refuse every one.
func TestZshOnlyExecutionsAreRefused(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"( glued to a $( )", "echo $(true)(E\n#" + T},
		{"( glued to $(( ))", "echo $((1))(E\n#" + T},
		{"( after an assignment that is an argument", "echo a=(E\n#" + T},
		{"( glued to a brace", "echo a{(E\n#" + T},
		{"( after in that is an argument", "echo in (E\n#" + T},
		{"( after an argument, newline then #", ": (E\n#" + T},
		{"( then | then #", ": (|#" + T},
		{"# right after a ( that is a subshell", "(#" + T},
		{"# right after a pipe", "echo a|#" + T},
		{"( after a ) , then #", "echo $(true)(#" + T},
		{"in after a quoted case subject", "case \"$x\" in (E\n#" + T},
	} {
		if !check(tc.cmd).Found() {
			t.Errorf("%s: the rule allowed %q", tc.name, tc.cmd)
		}
	}
}

// The other direction: a ( that is syntax in every shell, followed by a
// single-quoted backtick that nothing runs, is allowed.
func TestShellParensDoNotOverRefuse(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"! then a subshell", "! (true) || echo '`x`'"},
		{"[[ then a group", "[[ ( -n x ) ]] && echo '`x`'"},
		{"coproc then a subshell", "coproc (cat); echo '`x`'"},
		{"a subshell", "(true); echo '`x`'"},
		{"a brace group", "{ (true); }; echo '`x`'"},
		{"a case pattern", "case x in (x) :;; esac; echo '`x`'"},
		{"an array", "a=(1 2); echo '`x`'"},
	} {
		if f := check(tc.cmd); f.Found() {
			t.Errorf("%s: the rule refused %q: %s", tc.name, tc.cmd, f.Why())
		}
	}
}

func TestLookShells(t *testing.T) {
	look := func(name string) (string, error) {
		if name == "dash" {
			return "", errors.New("not found")
		}
		return "/bin/" + name, nil
	}
	for _, tc := range []struct {
		name, required               string
		shells, absent, missingNamed []string
	}{
		{"nothing required, dash absent", "", []string{"/bin/bash", "/bin/zsh"}, []string{"dash"}, nil},
		{"a required shell is absent", "bash, dash,zsh", []string{"/bin/bash", "/bin/zsh"}, []string{"dash"}, []string{"dash"}},
		{"all required are present", "bash,zsh", []string{"/bin/bash", "/bin/zsh"}, []string{"dash"}, nil},
	} {
		shells, absent, missing := lookShells(tc.required, look)
		if !reflect.DeepEqual(shells, tc.shells) || !reflect.DeepEqual(absent, tc.absent) || !reflect.DeepEqual(missing, tc.missingNamed) {
			t.Errorf("%s: got %v %v %v, want %v %v %v", tc.name, shells, absent, missing, tc.shells, tc.absent, tc.missingNamed)
		}
	}
}
