package sshweak

import (
	"strings"
	"testing"
)

// ⛔ The first row is the measured command (2026-11-22): the host key differed
// from known_hosts, the host rejected our key, and ssh fell back to a password
// prompt, with the agent forwarded by `Host *`. A command-line -o overrides the
// operator's ssh config, so only a parse of the command can refuse it.
func TestWeakeningsAreRefused(t *testing.T) {
	for name, cmd := range map[string]string{
		"the measured command":     "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null delavennat@cfarm433.cfarm.net uptime",
		"strict no":                "ssh -o StrictHostKeyChecking=no h uptime",
		"strict off":               "ssh -o StrictHostKeyChecking=off h uptime",
		"strict accept-new":        "ssh -o StrictHostKeyChecking=accept-new h uptime",
		"case-insensitive name":    "ssh -o stricthostkeychecking=NO h uptime",
		"attached -o":              "ssh -oStrictHostKeyChecking=no h uptime",
		"quoted with a space":      `ssh -o "StrictHostKeyChecking no" h uptime`,
		"known hosts to /dev/null": "ssh -o UserKnownHostsFile=/dev/null h uptime",
		"global known hosts":       "ssh -o GlobalKnownHostsFile=/dev/null h uptime",
		"password yes":             "ssh -o PasswordAuthentication=yes h uptime",
		"kbd interactive yes":      "ssh -o KbdInteractiveAuthentication=yes h uptime",
		"preferred password":       "ssh -o PreferredAuthentications=password h uptime",
		"preferred in a list":      "ssh -o PreferredAuthentications=publickey,keyboard-interactive h uptime",
		"forward agent yes":        "ssh -o ForwardAgent=yes h uptime",
		"forward agent -A":         "ssh -A h uptime",
		"forward agent in cluster": "ssh -vAN h",
		"scp":                      "scp -o StrictHostKeyChecking=no f h:/tmp/",
		"sftp":                     "sftp -oUserKnownHostsFile=/dev/null h",
		"rsync -e":                 `rsync -e "ssh -o StrictHostKeyChecking=no" a h:b`,
		"rsync -e with -A":         `rsync -e "ssh -A" a h:b`,
		"after cd and &&":          "cd /x && ssh -o StrictHostKeyChecking=no h uptime",
		"after a semicolon":        "true; ssh -o StrictHostKeyChecking=no h uptime",
		"behind sudo":              "sudo ssh -o StrictHostKeyChecking=no h uptime",
		"behind env":               "env FOO=1 ssh -o StrictHostKeyChecking=no h uptime",
		"inside bash -c":           `bash -c "ssh -o StrictHostKeyChecking=no h uptime"`,
		"inside sh -c single":      `sh -c 'cd /x && ssh -o UserKnownHostsFile=/dev/null h uptime'`,
		"inside eval":              `eval "ssh -A h uptime"`,
		"inside a substitution":    "echo $(ssh -o StrictHostKeyChecking=no h uptime)",
		"full path":                "/usr/bin/ssh -o StrictHostKeyChecking=no h uptime",
		"sshpass":                  "sshpass -p x ssh h uptime",
		"sshpass behind sudo":      "sudo sshpass -f f ssh h uptime",
		"askpass assigned":         "SSH_ASKPASS=/x/p ssh h uptime",
		"askpass require assigned": "SSH_ASKPASS_REQUIRE=force ssh h uptime",
		"askpass exported":         "export SSH_ASKPASS=/x/p && ssh h uptime",
		"askpass behind env":       "env SSH_ASKPASS=/x/p ssh h uptime",
		"-o after the host":        "ssh h -o StrictHostKeyChecking=no uptime",
		"a stray =x in front":      "=x ssh -A h uptime",
		"an empty word first":      "'' ssh -A h uptime",
		"forward agent a path":     "ssh -o ForwardAgent=/tmp/agent.sock h uptime",
	} {
		f := Check(cmd)
		if !f.Found() {
			t.Errorf("%s: not refused: %q", name, cmd)
			continue
		}
		if f.Option == "" || f.Match == "" {
			t.Errorf("%s: the finding must name the option and the text: %+v", name, f)
		}
	}
}

func TestOrdinarySSHIsNotRefused(t *testing.T) {
	for name, cmd := range map[string]string{
		"plain":                       "ssh linuxone 'uptime'",
		"with a port and a key":       "ssh -p 22 -i ~/.ssh/id_ed25519 u@h uptime",
		"strict yes":                  "ssh -o StrictHostKeyChecking=yes h uptime",
		"password no":                 "ssh -o PasswordAuthentication=no h uptime",
		"kbd no":                      "ssh -o KbdInteractiveAuthentication=no h uptime",
		"forward agent no":            "ssh -o ForwardAgent=no h uptime",
		"preferred publickey":         "ssh -o PreferredAuthentications=publickey h uptime",
		"batch mode":                  "ssh -o BatchMode=yes h uptime",
		"known hosts a real file":     "ssh -o UserKnownHostsFile=~/.ssh/known_hosts2 h uptime",
		"-l with an A in the user":    "ssh -lAlice h uptime",
		"-l then a user":              "ssh -l Alice h uptime",
		"scp":                         "scp -p f h:/tmp/",
		"rsync archive":               "rsync -a a h:b",
		"rsync acls":                  "rsync -A a b",
		"rsync -e plain ssh":          `rsync -e "ssh -p 22" a h:b`,
		"a mention in echo":           "echo ssh -o StrictHostKeyChecking=no",
		"a commit message":            "git commit -m 'never ssh -o StrictHostKeyChecking=no'",
		"a grep for the option":       "git grep -n StrictHostKeyChecking=no",
		"a quoted mention in bash -c": `bash -c "echo hello"`,
		"a host named vA":             "ssh vA uptime",
		"a long option":               "ssh --Alpha h",
		"-o with a bare name":         "ssh -o BatchMode",
		"an unrelated A flag":         "ls -A",
		"an ordinary assignment":      "FOO=1 ssh h uptime",
		"askpass read, not assigned":  `echo "$SSH_ASKPASS"`,
		"empty":                       "",
	} {
		if f := Check(cmd); f.Found() {
			t.Errorf("%s: refused: %q (%+v)", name, cmd, f)
		}
	}
}

func TestTheRefusalNamesTheOptionAndSaysStop(t *testing.T) {
	f := Check("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null h uptime")
	if f.Option != "StrictHostKeyChecking" {
		t.Fatalf("Option = %q, want the first one found", f.Option)
	}
	if !strings.Contains(f.Match, "StrictHostKeyChecking=no") {
		t.Errorf("Match = %q", f.Match)
	}
	if !strings.Contains(f.Why(), "StrictHostKeyChecking") {
		t.Errorf("Why = %q", f.Why())
	}
	for _, want := range []string{"STOP", "report", "key"} {
		if !strings.Contains(Advice, want) {
			t.Errorf("Advice does not say %q:\n%s", want, Advice)
		}
	}
}

func FuzzCheck(f *testing.F) {
	for _, s := range []string{"", "ssh -o", "ssh -o X", `ssh -o "`, "ssh -A", "sshpass", "SSH_ASKPASS=", "$(", "`", "rsync -e"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = Check(s)
	})
}
