# gitsafe — guards that do not rely on remembering

Two things went wrong on one machine, and neither was fixed by resolving to be
careful.

A token was written into a push URL, and git echoed it back. Twice. It had to be
revoked and reissued both times.

A tested, green change went straight onto `main` — no branch, no pull request,
no checks run against it, read by nobody.

Documentation asks. A hook refuses. These are the tools that refuse.

## `ghrelease` — merge and tag as one thing

    ghrelease [owner/repo] <pr-number> <tag>

Merging and tagging were two commands, and three times in one day the tag
landed on a commit that did not contain the merge. Every time the shape was the
same:

    ghmerge 21 | tail -2 && git tag v0.12.0 origin/main && gitpush origin v0.12.0

`ghmerge` refuses correctly and exits non-zero. The PIPE throws that away: a
pipeline's status is its LAST command's, and `tail` always succeeds. So the
refusal printed, the chain carried on, and a version tag was published pointing
at a `main` that did not have the fix in it. A published tag cannot be moved --
the module proxy has it -- so each mistake cost a version number permanently.

A rule against this did not work. It was written down and broken three times in
one day. So the two steps became one command, with one exit status and nothing
between them to drop it.

It tags the pull request's **merge commit**, by hash, never a branch name:
tagging `origin/main` is what went wrong, and a merge commit cannot be the wrong
one. It refuses a tag that already exists, and it checks that BEFORE merging, so
a refusal never leaves a merged pull request with no release. It runs `ghmerge`
rather than copying its rules, because two answers to "may this be merged" is
how one of them ends up wrong.

## What is here

| | |
|---|---|
| `git-pre-push-guard` | The **global** pre-push hook. Refuses a URL that carries a credential, and refuses a write to the branch pull requests land on — whoever runs git, and whatever they run it with. |
| `gitpush` | Pushes without a token ever reaching a command line: it **names** a credential helper rather than reading the secret, refuses a remote URL that carries one, and redacts what it prints by shape. Takes the same arguments as the command it replaces. |
| `git-credential-tokenfile` | A minimal credential helper: serves a token file to git over a pipe. Answers only `get`, only for one host, and refuses to write when stdout is a terminal. Rename it for your own account — git finds helpers by the `git-credential-` prefix. |
| `ghmerge` | Merges one pull request, and only on evidence: a check actually ran, every check that ran passed, and GitHub says it is mergeable. "Nothing failing" is not "everything passed" -- a pull request with no merge ref never runs a workflow, and the silence reads as green. |
| `ghscopes` | Says which account a token belongs to and what it may do, exiting non-zero if a demanded scope is missing. Check a token's scopes with this, **never** by printing it. |
| `guard-bash` | Refuses a shell command that would put a secret on a command line, **before it runs**. An agent harness hook: it reads the command on stdin and answers with a deny. The rule it enforces was written down in three places and broken anyway — see below. |

`redact` and `protect` are the two libraries under them: one hides secrets by
their shape wherever they appear, the other answers "does this push write the
branch pull requests land on".

## Installing

```
go install github.com/go-gitsafe/gitsafe/cmd/gitpush@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghmerge@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghscopes@latest
go install github.com/go-gitsafe/gitsafe/cmd/git-pre-push-guard@latest
```

The hook goes where git looks for hooks in every repository:

```
git config --global core.hooksPath ~/.config/git/hooks
cp "$(go env GOPATH)/bin/git-pre-push-guard" ~/.config/git/hooks/pre-push
```

## What the hook refuses, and what it deliberately allows

**A URL that carries a credential.** The offending string is never printed —
repeating a secret in order to complain about it is the mistake itself.

**A write to the default branch.** Work goes onto a branch and lands through a
pull request, so the checks run against it and somebody can read it first. The
refusal prints the recipe rather than only saying no.

A guard that gets in the way is a guard that gets switched off, so three things
go through untouched:

- **Tags.** A release tag is pushed to a repository whose default branch is
  protected all day long.
- **Creating** that branch. A repository whose `main` does not exist yet has
  nothing to open a pull request against.
- `GITSAFE_ALLOW_DEFAULT_BRANCH=1` in front of **one** command — an act, not an
  oversight, and it says in the transcript what was done.

`main`, `master`, and whatever the remote's own HEAD points at are protected.
The remote not answering is not fatal: the two usual names are defended anyway,
so this still works on a train.

## Chaining

Setting `core.hooksPath` makes git look **only** there, which would silently
disable any hook a repository installs for itself. The guard runs the
repository's own `pre-push` afterwards, and replays the ref list it read on
stdin — a hook that swallowed its own input would leave the local one deciding
on silence.

## Why a credential helper rather than a URL

`git -c credential.helper=X` **appends** to the helper list, and git asks them
in order — so an already-configured helper answers first and yours is never
consulted. That is not theory: it is what sent somebody to a URL in the first
place. `gitpush` clears the list at both scopes before naming its own.

Pure Go, no cgo, no dependencies outside the standard library. BSD-3-Clause.


## `guard-bash`, and why a written rule was not enough

The rule against a secret on a command line is in this README, in the machine's
instructions, and in a memory file. It was read, understood, and broken anyway:
the same operator typed

    curl -H "Authorization: Bearer $(cat ~/.github-token)" ...

about thirty times in one session, hours after re-reading the rule and writing
it down again. It reads as careful, because the secret is never typed. It is
not: the substitution runs first, so what reaches `execve` — and the process
list, and every log of what ran — is the token itself.

A rule that has to be remembered is a rule that will eventually be forgotten.

    # ~/.claude/settings.json
    {"hooks": {"PreToolUse": [{"matcher": "Bash",
      "hooks": [{"type": "command", "command": "guard-bash", "timeout": 10}]}]}}

### What it refuses, and what it must not

The decision is in `secretarg`, as a package, so it can be held to a table of
cases — and the half that matters most is the one it must **allow**:

    gh api repos/o/r --jq .full_name        # the tool holds its own credential
    { printf 'header = "A: '; tr -d '\n' < ~/.token; } | curl -K -
    wc -c < ~/.github-token                 # a property, never the value

A guard that refuses harmless commands is one people learn to work around, and
then it protects nothing at all. This machine has already had that happen to a
rule that matched the word `push` too broadly and refused `git stash push`.

The first version of this rule proved the point within five minutes: it refused
the command that was **writing the note about it**, because the prose quotes the
forbidden form. The fix is a distinction the shell already makes — the body of a
heredoc whose tag is quoted (`<<'EOF'`) is never expanded, so text that quotes
the form is not the form. An unquoted `<<EOF` does expand, and is still scanned.

### It fails open, on purpose

Input it cannot read means *no opinion*, not approval and not a block. A guard
that failed closed on its own bug would stop every command on the machine.
