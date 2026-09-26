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

`credurl`, `credfix`, `redact`, `protect` and `ghauth` are the libraries under
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

```
go install github.com/go-gitsafe/gitsafe/cmd/gitpush@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghmerge@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghnew@latest
go install github.com/go-gitsafe/gitsafe/cmd/ghscopes@latest
go install github.com/go-gitsafe/gitsafe/cmd/git-pre-push-guard@latest
go install github.com/go-gitsafe/gitsafe/cmd/credscan@latest
go install github.com/go-gitsafe/gitsafe/cmd/git-post-checkout-guard@latest
```

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
| 3 | **the run established nothing** — no checkout found, or one it could not read. Not "clean". |

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
