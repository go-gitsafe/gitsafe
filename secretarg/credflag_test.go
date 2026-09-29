package secretarg

import "testing"

// TestAFlagThatNamesASecretIsRefused.
//
// The other rules are keyed on the VALUE's shape, so a credential with no
// issuer prefix walks past them — and most credentials have no prefix.
// Measured 2026-09-29, before this rule existed, six of these seven were
// allowed and only the ghp_ one refused.
//
// The first is what prompted it. Registering a self-hosted runner needs
// `config.sh --token`, and a runner registration token is 29 bare uppercase
// characters: a rule on THAT shape would refuse any base32 digest. The flag is
// what this matches, and it is the honest signal — whoever wrote `--token`
// said what the next word is.
func TestAFlagThatNamesASecretIsRefused(t *testing.T) {
	// Not a real token; 29 uppercase characters is the shape of one.
	runner := "AABF3JGZDX3P5PMEXLND6TS6FCWO6"
	for _, c := range []string{
		"./config.sh --url https://github.com/o/r --token " + runner,
		"./config.sh --token " + runner + " --unattended",
		"mysql --password=hunter2hunter2",
		"aws configure --secret myverysecretvalue",
		"some-tool --api-key abcdefghijklmnop",
		"some-tool --api_key abcdefghijklmnop",
		"some-tool --access-key abcdefghijklmnop",
		"some-tool --client-secret abcdefghijklmnop",
		"some-tool --auth-token abcdefghijklmnop",
		"gh-like -token abcdefghijklmnop",
	} {
		if f := Check(c); !f.Found() {
			t.Errorf("allowed, and it should not be: %s", c)
		} else if f.Rule != "flag" && f.Rule != "literal" {
			t.Errorf("rule = %q for %s", f.Rule, c)
		}
	}
}

// TestTheCredentialFlagRuleLeavesTheSAFEFormsAlone.
//
// A guard that refuses harmless commands is one people learn to work around,
// and this machine has already had that happen to a rule matching "push" too
// broadly. Every line here is a way of NOT disclosing a secret, or a person
// writing about one, and each must stay allowed.
func TestTheCredentialFlagRuleLeavesTheSAFEFormsAlone(t *testing.T) {
	for _, c := range []string{
		// The safe forms of the very flags this rule matches.
		"docker login --password-stdin -u me",
		"./config.sh --token-file ~/.runner-token",
		"some-tool --token-file /etc/tok",
		"psql --password",   // prompts; no value on the line
		"some-tool --token", // likewise
		// A path for the tool to read, not the value.
		"some-tool --api-key ./key.txt",
		"some-tool --api-key ../secrets/key",
		"some-tool --token /etc/token",
		"some-tool --token ~/.token",
		// Documentation and placeholders.
		"echo 'pass --token <YOUR_TOKEN> to config.sh'",
		"some-tool --token <TOKEN>",
		"some-tool --token YOUR_TOKEN",
		"some-tool --token {{ secrets.GITHUB_TOKEN }}",
		"some-tool --token xxxxxxxx",
		"some-tool --token -",
		// A flag whose NAME merely starts the same way.
		"gh pr list --template '{{.title}}'",
		"go test -timeout 30s ./...",
		// The short flags this rule deliberately does NOT know, because they
		// mean something else far more often than they mean a credential.
		"mkdir -p /tmp/build/x",
		"docker run -u 1000:1000 alpine",
		"cp -p a b",
		"ssh -p 2222 host",
	} {
		if f := Check(c); f.Found() {
			t.Errorf("refused, and it should not be: %s\n  rule=%s match=%q", c, f.Rule, f.Match)
		}
	}
}

// The refusal has to point at something rather than assert, so a person can
// see what was matched without the tool printing the whole command back.
func TestTheCredentialFlagRuleSaysWhatItMatched(t *testing.T) {
	f := Check("./config.sh --url https://github.com/o/r --token AABF3JGZDX3P5PMEXLND6TS6FCWO6")
	if f.Rule != "flag" {
		t.Fatalf("rule = %q", f.Rule)
	}
	if f.Match != "--token AABF3JGZDX3P5PMEXLND6TS6FCWO6" {
		t.Errorf("match = %q", f.Match)
	}
	if f.Why == "" {
		t.Error("a refusal with no reason is an obstacle rather than a rule")
	}
}
