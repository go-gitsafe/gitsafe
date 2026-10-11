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

### A fourth time, in a new spelling — and what the dry run says now

On 2026-10-10 it happened again, without a pipe:

    ghmerge go-gitsafe/gitsafe 40                   # merged, 8/8 green
    SHA=$(gh api repos/.../commits/main --jq .sha)  # a MOVING ref
    gh api -X POST .../git/refs -f sha="$SHA"

A peer merged another pull request between the verification and the tag, so
`v0.6.1` landed on a commit whose checks were still running. It happened to be
green. `commits/main` is just another name for "tagging a branch".

**This command already handled that case.** `ghmerge` prints "is already merged"
and exits **0** on a merged pull request, so `ghrelease <repo> <pr> <tag>` would
have tagged `#40`'s own merge commit. What sent someone around it was the dry
run's wording: it said "would merge X, then tag its merge commit TAG" whatever
the state, which reads as "will try to merge" for something already merged.

So the dry run now **names the commit it would tag**:

    $ ghrelease -dry-run go-gitsafe/gitsafe 40 v0.6.2
    go-gitsafe/gitsafe#40 is already merged; would tag v0.6.2 at 31b41b45, its merge commit

    $ ghrelease -dry-run go-gitsafe/gitsafe 41 v0.6.2
    would merge go-gitsafe/gitsafe#41, then tag v0.6.2 at the merge commit that produces — it does not exist yet

That is the step that was missing: a hash a person can compare with the one whose
checks they watched. When it cannot read the pull request it says so on stderr and
still exits 0 — refusing there would make `-dry-run` useless exactly when someone
is being careful.

## What is here

| | |
|---|---|
| `git-pre-push-guard` | The **global** pre-push hook. Refuses a URL that carries a credential, a write to the branch pull requests land on, and a push that publishes an undeclared nested repository — whoever runs git, and whatever they run it with. |
| `gitpush` | Pushes without a token ever reaching a command line: it **names** a credential helper rather than reading the secret, refuses a remote URL that carries one, and redacts what it prints by shape. Takes the same arguments as the command it replaces. |
| `git-credential-tokenfile` | A minimal credential helper: serves a token file to git over a pipe. Answers only `get`, only for one host, and refuses to write when stdout is a terminal. Rename it for your own account — git finds helpers by the `git-credential-` prefix. |
| `ghmerge` | Merges one pull request, and only on evidence: a check actually ran, every check that ran passed, and GitHub says it is mergeable. "Nothing failing" is not "everything passed" -- a pull request with no merge ref never runs a workflow, and the silence reads as green. |
| `ghnew` | Creates a repository that **already has a default branch**, so no commit of yours ever has to land on it unreviewed. An empty repository has no branches, `gh pr create` has no base, and the way out that presents itself is pushing straight to the default branch — four repositories were bootstrapped that way here in one afternoon. It asks GitHub to write the first commit, then **looks** to confirm the branch is there before saying it worked. There is no flag to skip that. |
| `ghscopes` | Says which account a token belongs to and what it may do, exiting non-zero if a demanded scope is missing. Check a token's scopes with this, **never** by printing it. |
| `ghpkg` | Lists and deletes the versions of a published package. Refuses to delete without `--yes`, printing what it would remove; refuses a tag that names no version or two; and **names the other tags on the manifest it is about to delete**, because several can point at one. |
| `guard-bash` | Refuses a shell command that would put a secret on a command line, **before it runs**. An agent harness hook: it reads the command on stdin and answers with a deny. The rule it enforces was written down in three places and broken anyway — see below. |
| `credscan` | Finds credentials embedded in git remote URLs across a whole machine, and strips them. Tells a secret from a username by SHAPE, so `ssh://git@github.com/…` is not a finding — and reports a credential's properties, never its value. Exits non-zero when it finds one, and exits 3 rather than 0 when it could not read what it walked. |
| `git-post-checkout-guard` | The **global** post-checkout hook. A clone is where a credentialed URL gets WRITTEN, and git has no hook before one, so this is the earliest a hook can run: it takes the credential out of .git/config before the next fetch echoes it, and says so while you are still looking. It cannot stop the clone — see what it does not cover, below. |
| `glscopes` | `ghscopes` for **GitLab**: which account a token belongs to, its scopes and when it expires — asked of the instance with the token in a header, never read out of the file. Exits non-zero if a demanded scope is missing, and knows GitLab's containments (`api` ⊃ `read_api`, `write_repository` ⊃ `read_repository`, but **not** `write_registry` ⊃ `read_registry`). |
| `glci` | How the latest **GitLab** pipeline of a branch, tag or merge request (`'!1'`) went, job by job, with the end of each failed job's log, cleaned and **masked**. `-watch` waits for the end. Exits 4 when there is **no pipeline at all**: nothing ran, so nothing passed. |

`credurl`, `credfix`, `redact`, `protect`, `ghauth` and `glauth` are the libraries under
them: one answers "is the part before the `@` a secret or a username" and holds
the one table of issuer prefixes every guard here asks, one walks a machine and
repairs what it finds, one hides
secrets by their shape wherever they appear, one answers "does this push write
the branch pull requests land on", and one reads a credential without ever
putting it where a second process could see it — and knows that GitHub's scopes
are a hierarchy, so a token ticked `write:packages` is not told it cannot read
them.

## A repository with no branch is how a commit reaches main unreviewed

`gh repo create` makes an empty repository: no commits, no branches, no default
branch. So `gh pr create` has nothing to open a pull request *against*, and the
first commit cannot go through one. What presents itself instead is a direct push
to `main` — and that is how four repositories got their first commit here in one
afternoon, none of them reviewed and none of them tested, because there was
nothing there yet to test.

The pre-push hook did not stop it either, and should not have: it refuses a write
to the branch pull requests land on, and a branch that does not exist yet is not
that.

There is a second trap in the same place. Push a **feature** branch first to an
empty repository and GitHub adopts *that* branch as the default. The repository
then has no `main` at all, and its default branch is named after whatever happened
to go up first. One of those four needed its default branch reset by hand
afterwards.

`ghnew` removes both by having GitHub write the first commit:

    ghnew -public go-compressions/adc "Apple Data Compression, pure Go"
    ghnew -private me/notes
    ghnew -public -clone go-filesystems/xar "the macOS .pkg container"

The repository arrives with `main`, a LICENSE and a README, all of them GitHub's
work rather than yours — so from your first line of code onward, everything is a
pull request.

Three things it refuses, and each is the interesting part:

- **It will not tell you it worked without looking.** `auto_init` is a request,
  not a guarantee: an invalid licence keyword gives a 201 for the repository and
  no initial commit. So it reads the ref back, retrying while GitHub finishes
  writing it, and if the branch never appears it says so and tells you **not** to
  push — which is the one moment that advice matters.
- **It insists on `-public` or `-private`.** Neither default is safe: one
  publishes something nobody asked to publish, the other quietly makes a
  repository the fleet's tooling cannot see.
- **It will not adopt a repository that already exists**, because "already there"
  says nothing about whether it has a default branch.

There is deliberately **no flag for creating one without an initial commit**. That
is the whole defect, and an option to switch it off would be an option to have it
back; a test asserts the flag does not exist. `gh repo create` is still there for
anyone who genuinely wants an empty repository.

## Installing

**Where they land.** `go install` writes to `GOBIN`, and `GOBIN` defaults to
`$(go env GOPATH)/bin` — which is **not necessarily a directory your `PATH`
contains**. On the machine these guards were written for it is not: six commands
built on 2026-10-08 sat unused in `~/go/bin` while the copies actually being run
were older ones in `~/.local/bin`. An install that lands outside `PATH` looks
exactly like an install that worked.

So say where, and check afterwards that the shell agrees:

```
export GOBIN=~/.local/bin          # or wherever your PATH looks

go install github.com/go-gitsafe/gitsafe/cmd/gitpush@latest
go install github.com/go-gitsafe/gitsafe/cmd/git-credential-tokenfile@latest
go install github.com/go-gitsafe/gitsafe/cmd/git-pre-push-guard@latest
go install github.com/go-gitsafe/gitsafe/cmd/git-post-checkout-guard@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghmerge@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghrelease@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghnew@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghscopes@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghpkg@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghrunner@latest
go install github.com/go-gitsafe/gitsafe/cmd/credscan@latest
go install github.com/go-gitsafe/gitsafe/cmd/reauthor@latest
go install github.com/go-gitsafe/gitsafe/cmd/guard-bash@latest
go install github.com/go-gitsafe/gitsafe/cmd/hookcheck@latest
go install github.com/go-gitsafe/gitsafe/cmd/glscopes@latest
go install github.com/go-gitsafe/gitsafe/cmd/glci@latest

which -a gitpush ghrelease git-credential-tokenfile   # one path each, the one you meant
```

⛔ **`git-credential-tokenfile` is not optional.** `gitpush` names `tokenfile` as
its credential helper, and git resolves that to `git-credential-tokenfile` on
`PATH`. Without it there is nothing to serve the token and the first push fails —
which is the moment someone reaches for a URL with a credential in it. This list
left it out, along with `ghrelease`, which has a section of its own above;
`TestTheInstallListNamesEveryCommand` now keeps the list and `cmd/` in step.

The hook goes where git looks for hooks in every repository:

```
git config --global core.hooksPath ~/.config/git/hooks
cp "$(go env GOPATH)/bin/git-pre-push-guard" ~/.config/git/hooks/pre-push
cp "$(go env GOPATH)/bin/git-post-checkout-guard" ~/.config/git/hooks/post-checkout
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

**A nested git repository nobody declared.** A directory that is itself a
repository is recorded as a GITLINK — a tree entry holding a commit id and
nothing else — and `git add -A` records one for every such directory it finds,
silently and all at once. Nineteen per-session agent worktrees under `.claude/`
went up to a public repository in a single commit that way, and had to be
rewritten and force-pushed back out.

The rule is structural rather than a list of directory names, because a list
would have to guess at `.claude`, `.idea`, `node_modules`, a checkout somebody
made in place — and would be blind to the twentieth. What is actually wrong is
narrower: **a gitlink that no `.gitmodules` declares is not a submodule, it is an
accident.** A real submodule is always declared, since that is the only way a
clone can restore it.

Three things go through untouched:

- **A declared submodule.** It is in `.gitmodules`, so it is deliberate.
- **A gitlink the remote already has.** A repository that has always carried one
  is not made worse by the next push; only the push that INTRODUCES one is
  refused. A guard that refused every push in such a repository would be
  switched off within the hour.
- `GITSAFE_ALLOW_GITLINK=1` in front of **one** command.

The refusal names each path and prints the recovery: `git rm -r --cached`, an
amend, and the `.gitignore` line that stops it coming back.

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


## `reauthor` — a personal address in a root commit, reachable from every tag

    reauthor -identities <file> [-to "Name <email>"] [-dry-run] [<repo>]

A sweep of 660 repositories on one machine in August 2026 found four identifiers
where only the public one should have been, in **31** repositories. Two causes: the
root commit of most of them carried a personal address under the right name — and a
root commit is reachable from every branch and every tag — plus a run of commits made
with `git -c user.name` in one session.

It rewrites the commits that are **yours** and leaves everybody else's byte for byte.
Trees, messages, parents (merges included), author dates and committer dates all
survive: it reads and writes raw commit objects rather than passing dates through a
format, so an offset cannot be normalised on the way through. The working tree comes
out identical and every Go module hash stays valid, because a module zip is built
from the tree and not from the commit.

### Your identities are an argument, never a list in here

The shell script this replaces carried seven personal addresses and a personal name
in its source. Publishing that would publish exactly what the tool removes — and a
list in a program is wrong for a second reason: it names values instead of describing
them, so it dies the day one changes. There is no default:

    # one per line, blank lines and # comments ignored
    email: someone@example.invalid
    name:  Some Name

A **name** matches whatever the address is: a commit under your name with a
stranger's address is still yours. An **address** matches whatever the name is, which
is the case the first sweep missed — a name being right is not evidence the address
is.

⛔ Only your own identities belong in that file. The shell version's final check
grepped for an institution's domain, and that would flag a contributor whose address
is at the same institution and whose single commit is precisely what this tool exists
to preserve. This one checks the identities you named, and nothing else.

### What it refuses

- uncommitted changes to **tracked** files; untracked ones are nobody's business and
  a build directory is no reason to stop;
- a second run on the same repository, with a lock directory in the git dir. Killing
  the shell that started a rewrite does not kill the rewrite: in August 2026 that left
  two of them on one repository, both heading for `update-ref` and a hard reset on the
  same refs. `mkdir` is the test and the claim in one step;
- finishing quietly if **any ref's tree** changed — not just HEAD's, because a branch
  moved onto the wrong commit leaves HEAD's tree intact and is still wrong;
- finishing quietly if any of your identities is still reachable from a branch or tag.

### The defect the port fixes, measured

The shell version moved annotated tags with `git tag -a <tag> -m "$subject"`, and
`%(contents:subject)` is the FIRST LINE. On the same fixture, a tag whose message had
a body:

    before          after the shell script      after this
    v0.1.0          v0.1.0                      v0.1.0
                                                (blank)
    A body that                                 A body that must survive.
    must survive.                               Two lines of it.
    Two lines of it.

Three lines gone, and it printed `1 commits rebuilt, tree unchanged, nothing left to
publish` — a success. This rewrites the tag **object**, so the message and the
tagger's date survive and only an owned tagger identity changes.

### `-scan` — which checkouts still carry you

    reauthor -identities <file> -scan <root>...

The rewrite fixes one repository. Nothing says which ones need it, and the only
reason the 31 were ever found is that somebody looked. So the detection half runs on
a schedule, the way `credscan scan` does:

    # once a day, and it says something only when it finds something
    reauthor -identities ~/.git-identities -scan ~/src >/tmp/ident.log 2>&1 \
      || mail -s "reauthor" you < /tmp/ident.log

It is read-only and exits non-zero when it finds any, so cron or a launchd job can
gate on it. It counts the **committer** as well as the author: a commit somebody else
wrote and you rebased carries your address as committer, and that is published just
as visibly.

⛔ **Completeness is reported separately from the answer.** A checkout that would not
answer and a directory that refused listing are counted and said out loud, because a
scan that could not read must not be reported as a clean machine. An empty repository
is neither: `git log` exits non-zero there and that is not a failure to read.

⛔ **And it says WHERE, because the total on its own is the wrong number to act on.**
The first real sweep read as *"350 repositories carry an identity, in 50724
commits"*. Grouped by where `origin` points, that same answer is:

    20355 commit(s) in  215 repo(s)   plmlab.math.cnrs.fr
    16992 commit(s) in   44 repo(s)   git.math.cnrs.fr
    13111 commit(s) in   88 repo(s)   github.com
      212 commit(s) in    1 repo(s)   ssh.github.com
       54 commit(s) in    2 repo(s)   (no remote — published nowhere)

259 of the 350 are on an institution's own forges, where that institution's address
belongs and is not a leak. The number to act on is **89** — the two GitHub rows added
together — and 13323 commits.

⛔ **The grouping is in the tool because the first attempt at it, by hand, was wrong
by a factor of forty.** It grouped by PATH and reported "12 repositories on
github.com, 280 commits", plus "71 with no remote". A path does not say where a
repository is published: almost all of those 71 have a remote (only **2** have none),
and dozens of repositories elsewhere in the tree — under `portail-maths`, `plm-dev`,
`localhost` — point at GitHub. Post-processing a tool's output to make it actionable
belongs inside the tool, where it is tested; done beside it, nothing checks it.

One nuance this surfaces and does not resolve: `github.com` and `ssh.github.com` are
the same forge under two hostnames (the latter is GitHub's SSH endpoint on port 443).
Normalising would mean a table of host aliases in the source — a list that names
values rather than describing them, which is what `-identities` exists to avoid — so
both rows stay and the reader adds them.

It groups by the **remote's host and not the path**, and names the host on each
repository's own line as well: a summary that counts 88 repositories without naming
them is not actionable, and the reader then groups the output by path to find them —
which is the mistake above. A remote that is a local path is named as such rather than counted as "no
remote". The URL is read with this repository's own `credurl`, which already knows the
scp form `git@host:path` — two parsers of the same thing is how one of them ends up
wrong.

⛔ It needs the roots spelled out. It will not walk `$HOME` by default.

### It does not push

Every hash changes, so publishing means force-pushing branches **and** tags:

    gitpush --force origin --all
    gitpush --force origin --tags

`--force`, not `--force-with-lease`: the lease on a rewritten tag is stale by
construction and the push is rejected.

⛔ **And do not fetch in between.** `git fetch --tags` brings the old tags back down
and silently undoes part of the rewrite — it cost two repositories their tags in
August 2026, from a command run only to see what the remote held. Ask the remote with
`git ls-remote`, which writes nothing.

## `credscan` — a token in 117 remote URLs for three months

    credscan scan  [root...]              report; default root is $HOME
    credscan clean <root...> [--write]    strip them; a dry run without --write

A GitHub classic personal access token sat in the `origin` **fetch and push** URL
of 117 local checkouts for three months. Git prints a remote URL on any fetch, so
it was disclosed from the first day. Nothing on the machine noticed, because
nothing was looking.

Two detectors were written by hand during that incident and **both were wrong, in
opposite directions.** They are the specification:

| what it matched | what it got wrong |
|---|---|
| `://user:TOKEN@host` | **missed 5 checkouts** whose URL was `://TOKEN@host`. A credential does not need a username in front of it. |
| any `://…@…` | **flagged 55 URLs that were never leaks**: `ssh://git@github.com/o/r`, `git@plmlab.math.cnrs.fr:team/repo`. `git` there is an SSH login. |

So telling a secret from a username IS the job, and getting it wrong in the
second direction is the more expensive one: a guard that calls everyday remotes
leaks gets switched off, and then nothing guards anything. The verdict is made on
the SHAPE of the userinfo, never on the presence of an `@`:

| | |
|---|---|
| known issuers | `ghp_ gho_ ghu_ ghs_ ghr_ github_pat_` (GitHub), `glpat- gldt- glrt-` (GitLab), `xox[bpars]-` (Slack), `AKIA ASIA` (AWS) |
| a password half | present and not a documented placeholder ⇒ a credential, whatever it looks like. There is no legitimate reason for that half to exist in a remote URL. |
| anything else, long | ≥ 24 characters, digits and both cases, a token charset, per-character entropy above a word's — or ≥ 32 hex digits, since a digest has only one case |
| known benign | the bare login `git`; and `oauth2`, `x-access-token`, `token`, `gitlab-ci-token`, **as a username** — those names say the password is the credential, so with a password half the same URL IS a leak and the password is what is reported |
| `git@host:path` | its userinfo is a login by construction — ssh is handed it as one — so the entropy fallback does not apply there. A known issuer prefix still does: nothing beginning `ghp_` is somebody's login. |

The six cases of the table above are a test, each asserted in **both**
directions. A table of leaks alone would have passed for the too-broad detector.

### It never prints what it found

A finding names the repository, the host, the configuration key, and the
credential's **properties**: issuer prefix, length, and the first 12 hex digits
of its SHA-256, so two findings can be told apart and the same credential can be
recognised in two places.

    remote.origin.pushurl: GitHub classic personal access token (ghp_…), 40 chars,
                           sha256:d5f9c7412cb8, in the username of a URL for github.com

Not the value. A tool that printed the secret it found would be the leak it is
reporting. The prefix comes from the table rather than from the input, so even
the printed prefix cannot echo something unknown — and a username is only ever
repeated when it is one of the benign names above, because echoing a userinfo
this decided was harmless would disclose it in exactly the case where the
decision was wrong.

### It says what it walked

    walked 899556 directories in 2m38s
    found 2155 checkouts, 2133 distinct configurations, 0 unreadable, 243 directories refused listing
    0 of them carry a credential: 0 URL(s) still do

Every run prints those counts, whether it found anything or not, and a
repository and its linked worktrees count once. A scan that could not read must
not report zero: a sweep here once printed "4 files" for 4413 because the
command it used had no such flag, and it read as a success.

| exit | |
|---|---|
| 0 | walked something, read all of it, found nothing |
| 1 | a credential is in a URL |
| 2 | usage |
| 3 | **found nothing and did not cover everything** — no checkout found, or one it could not read. Not "clean". A finding outranks it: a full `$HOME` scan here found 3 credentials *and* 21 checkouts it could not read, and answering only "inconclusive" would have buried the credentials. |

Exit 3 is not pedantry. On this machine `git` can be an Xcode stub that prints a
licence refusal and still exits 0, and a repository whose configuration comes
back empty is impossible — every one has `core.repositoryformatversion`. So an
empty answer is reported as unreadable, with whatever git said on stderr while
succeeding, which every caller that checks only the exit status throws away.

### `clean` reads back what it wrote

`clean` strips the userinfo, leaving `https://host/owner/repo` so git falls back
to the credential helper, and then **reads each value back out of git** before
counting it repaired. A repair that reports success without looking is how this
lasted three months.

    credentialed URLs: 2 before, 0 after

It is a dry run unless given `--write`, it is idempotent, and `--write` refuses
to default to `$HOME`: this machine has 2155 checkouts under it, and somebody
repairing one did not ask for the other 2154. The credential never reaches a
command line — only the CLEANED value is passed to git, which is why the rewrite
uses `--replace-all` on a key rather than any form that names the old value.

It rewrites any single-valued key whose VALUE is a credentialed URL, whatever the
key is called. That is not generality for its own sake: this started out
restricted to `remote.<name>.url` and `.pushurl`, and the first real scan found
three checkouts still carrying a token in `branch.<name>.remote` — which git
allows to be a URL — months after every remote URL on the machine had been
cleaned. A repair aimed at the key somebody thought of is a repair that leaves
the others.

Three findings it reports and will **not** repair, because each would be a guess:
a credential in the configuration KEY (`url.https://TOKEN@host/.insteadOf` is a
legal rewrite rule — where it should point instead is a decision), an scp-style
URL (dropping the user leaves `host:path`, which is ambiguous), and a key holding
more than one URL (`--replace-all` would collapse them).

## Prevention, and exactly where it stops

A push was already refused, by `git-pre-push-guard`, and that is where the first
two leaks here were caught. But **a push is not where the URL is written.**
`git clone https://TOKEN@host/o/r` writes it into `.git/config`, prints it on
that first fetch, and prints it again on every fetch afterwards. In this incident
there were three months between the clone and anyone looking.

Git has no pre-clone hook and no hook on a configuration write. The earliest a
hook can run at all is `post-checkout`, which git runs at the end of a clone —
after the clone, after the token has been printed once. `git-post-checkout-guard`
takes the credential out of the file at that moment, reads the value back, prints
its properties, and exits non-zero so the message has something to stop on.

**What that does not cover.** A guard whose limits are unstated is how this went
unnoticed for three months:

- `git clone --bare`, `--mirror` and `--no-checkout` run **no post-checkout hook
  at all**. Nothing catches those.
- `git fetch https://TOKEN@host/…`, `git pull <url>`, `git ls-remote <url>`:
  nothing is written to config, so no hook runs, and git may still echo the URL.
- `git remote add` and `git remote set-url` with a credential: git has no hook on
  a configuration write. Not covered until the next scan.
- A repository that sets its own `core.hooksPath` replaces the global one, and
  this stops applying there.
- Only the LOCAL configuration is read: a credential in the global config, in
  `~/.git-credentials` or in `.netrc` is not looked at.
- And it is remediation rather than prevention. By the time it runs, the
  credential has been on a command line and in git's output. **It must still be
  revoked.**

For every case above the barrier is detection on a schedule — `credscan scan`
exits non-zero, so cron or a launchd job gates on it — plus the push refusal that
was already here. That is the honest answer, and it is a weaker one than the
word "prevention" suggests. The only thing that removes the class of mistake is
never putting a credential in a URL: `gitpush` and a credential helper do the
same job with nothing to leak.

    # once a day, and it says something only when it finds something
    credscan scan >/tmp/credscan.log 2>&1 || mail -s "credscan" you < /tmp/credscan.log

`guard-bash` is the one barrier that is genuinely in front of the mistake: it
refuses a command line carrying a token before it runs, so `git clone
https://TOKEN@host/…` never executes. It covers only commands that go through
the agent harness, and nothing a person types in a terminal.

### And one case this list used to omit: these tools themselves

`guard-bash` asks `secretarg`'s question of commands the harness runs. Nothing
asked it of gitsafe. Six commands read the token — `ghscopes`, `ghnew`, `ghpkg`,
`ghmerge`, `ghrunner` — and an audit on 2026-10-10 found every one of them passing
it only to an HTTP helper. A reading is not a guarantee, and the pressure is real:
`actions/runner`'s `config.sh` takes the registration token as `--token`, which is
why `ghrunner` opens with twenty lines about avoiding it.

So it is enforced. `secretarg`'s `TestNoToolPutsTheTokenOnACommandLineOrInTheEnvironment`
parses every non-test `.go` file in the module and fails if an identifier holding the
value of `ghauth.Read` reaches an argument of `exec.Command`/`exec.CommandContext`,
`os.Setenv`, or an assignment to `.Env` — the whole subtree of each argument, so an
intervening `Sprintf` hides nothing. It also fails if it parsed too few files, or if
fewer than four of them hold a token, because a scan that reads nothing reports
nothing and passes.

**How to talk about a secret without holding it.** The pattern the rest of this
repository uses, and the one to copy elsewhere: `credurl.Finding` carries the shape,
the issuer's marker *from a fixed table*, the length, the first twelve hex digits of
a SHA-256, which half of the userinfo it sat in, and the host. That is enough to tell
two credentials apart, to recognise the same one in two places, and to confirm a
revocation covered the right value. None of it is the value.

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

## Discarding uncommitted work

`guard-bash` refuses a command that throws away what exists only in the working
tree. There is no reflog for a file that was never committed.

It happened three times here, in three spellings, and the written rule stopped
none of them: `git checkout -q -- .` slipped into a chain that prepared a commit
and erased the fix, leaving only the untracked test — the commit, the push and
the pull request were all green and the content was wrong; `git checkout <branch>
-- <file>` destroyed uncommitted tests twice in one session; and `git checkout
main` in a background watcher took an edit away with the branch it had just
deleted.

The rule names the **act**, not one spelling of it — `git checkout -- …`,
`git checkout .`, `git restore …`, `git reset --hard`, `git clean -f`. A guard
that refused only the first would send whoever hit it to the next one.

What goes through, because a guard that refuses ordinary work is one people learn
to route around:

```
git checkout -b a-branch     # switching and creating: git protects those itself
git checkout main            # and refuses when it would lose data
git restore --staged f.go    # unstages; the working tree is untouched
git reset --soft HEAD~       # moves the branch, keeps the tree
git stash push -- f.go       # the safe equivalent, which the refusal names
```

Writing *about* the forbidden form is not the forbidden form: the body of a
heredoc whose tag is quoted is never expanded, so a commit message or a note that
quotes it goes through. `GITSAFE_ALLOW_DISCARD=1` in front of one command is the
deliberate way past.


## GitLab: `glscopes` and `glci`

The first look at a GitLab token on this machine printed it. The token file was
expected to hold `PRIVATE-TOKEN: <token>`, it held the bare token, and a format
check that printed "the header name" printed the token. Worse, the masking that
should have caught it did not: GitLab's routable tokens end in
`.<version>.<length+crc>`, and the shape stopped at the first dot, leaving the
end of the token in clear. Both are fixed here, once, in `glauth` and `credurl`.

A token lives in a file and goes nowhere but a request header:

    ~/.gitlab-token-<host>      one per GitLab instance, e.g. ~/.gitlab-token-plmlab.math.cnrs.fr
    ~/.gitlab-token             the fallback

The file may hold the bare token or a `PRIVATE-TOKEN: <token>` line. Both
commands find the instance and the project from the clone's `origin` remote, and
refuse a remote URL that carries a credential rather than repeat it.

    glscopes read_api            may the token for this clone's instance read the API?
    glci                         the pipeline of the current branch
    glci -watch '!3'             wait for merge request !3's pipeline to finish

`glci` exit statuses are part of its interface: 0 success, 1 failed or canceled,
3 not finished (a `manual` job included), 4 **no pipeline**, 2 usage or refusal.
A refusal says which one it was: 401 is the token (revoked, expired, another
instance), 403 a scope or a role, and 404 may be a private project the token's
account cannot see — GitLab answers 404 rather than 403 there.

