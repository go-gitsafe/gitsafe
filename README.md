# gitsafe — guards that do not rely on remembering

Two things went wrong on one machine, and neither was fixed by resolving to be
careful.

A token was written into a push URL, and git echoed it back. Twice. It had to be
revoked and reissued both times.

A tested, green change went straight onto `main` — no branch, no pull request,
no checks run against it, read by nobody.

Documentation asks. A hook refuses. These are the tools that refuse.

## What is here

| | |
|---|---|
| `git-pre-push-guard` | The **global** pre-push hook. Refuses a URL that carries a credential, and refuses a write to the branch pull requests land on — whoever runs git, and whatever they run it with. |
| `gitpush` | Pushes without a token ever reaching a command line: it **names** a credential helper rather than reading the secret, refuses a remote URL that carries one, and redacts what it prints by shape. Takes the same arguments as the command it replaces. |
| `git-credential-tokenfile` | A minimal credential helper: serves a token file to git over a pipe. Answers only `get`, only for one host, and refuses to write when stdout is a terminal. Rename it for your own account — git finds helpers by the `git-credential-` prefix. |
| `ghmerge` | Merges one pull request, and only on evidence: a check actually ran, every check that ran passed, and GitHub says it is mergeable. "Nothing failing" is not "everything passed" -- a pull request with no merge ref never runs a workflow, and the silence reads as green. |
| `ghscopes` | Says which account a token belongs to and what it may do, exiting non-zero if a demanded scope is missing. Check a token's scopes with this, **never** by printing it. |

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
