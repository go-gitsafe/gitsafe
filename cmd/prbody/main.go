// Command prbody answers, before a pull request exists, the question ghmerge
// will ask after it does: does every issue this body mentions say what happens
// to it?
//
// # Why it exists
//
// The gate works. On 2026-11-09 it refused four pull requests in one session —
// go-zpkg/brand#2, go-zpkg/zbk#448, go-zpkg/bottle#220 and go-zpkg/zbk#449 —
// each for the same reason: the body cited issues as EVIDENCE ("the defects a
// verbatim copy would carry forward") and citing is not a disposition. Every
// refusal cost an edit, a re-read and a second merge attempt, after CI had
// already run.
//
// ⛔⛔ AND THE FOURTH TAUGHT SOMETHING I FIRST GOT WRONG, WHICH IS THE WHOLE
// ARGUMENT FOR THE TOOL. That body said
//
//	Refs go-zpkg/pantry-overlay#89, #88
//
// and the gate still refused `go-zpkg/pantry-overlay#88`. I wrote here, in this
// comment, that the cause was the comma — "a keyword governs the reference that
// FOLLOWS it". Then I ran this command against that body, and it answered:
//
//	refs    go-zpkg/pantry-overlay#89
//	refs    go-zpkg/zbk#88
//
// The comma carries the keyword perfectly well. What it does NOT carry is the
// REPOSITORY: a bare `#88` resolves to the pull request's OWN repo, so that
// line dispositioned `go-zpkg/zbk#88` — a different issue that happens to share
// a number — while `go-zpkg/pantry-overlay#88`, spelled in full further up the
// prose, stayed bare.
//
// A cross-repository trap, not a punctuation one. I had the mechanism backwards
// and the only thing that corrected it was asking the code that enforces the
// rule. That is what this command is for.
//
// # Judge and subject are the same code
//
// This calls [issuegate.Parse], which is what ghmerge calls. A second
// implementation of the rule would drift, and a tool that disagreed with the
// gate would be worse than no tool: it would teach a body is fine that then is
// refused.
//
// # What it does NOT do
//
// It does not ask GitHub anything. Whether an issue is open, closed or a pull
// request needs the network and is ghmerge's business; whether a reference
// carries a disposition is text, and text is what can be checked before the
// body is ever sent. That split is deliberate: a pre-flight check that needed a
// token and a round trip is a check nobody runs.
//
//	prbody /tmp/body.md --repo go-zpkg/zbk
//
// Exit status: 0 every reference says what happens to it, 1 at least one does
// not, 2 the file could not be read.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/go-gitsafe/gitsafe/cliflags"
	"github.com/go-gitsafe/gitsafe/issuegate"
	"github.com/spf13/cobra"
)

// osExit is a seam: the tests drive main() and must not end the test binary.
var osExit = os.Exit

func main() {
	if code := run(os.Args[1:], os.Stdout, os.Stderr); code != 0 {
		osExit(code)
	}
}

// run builds the command and drives it through the repository's adapter, so the
// tests reach the argument handling a person gets — single-dash spellings
// included, and `-h` answering 0.
//
// ⛔ THE FIRST VERSION CALLED c.Execute() DIRECTLY, and the repository's own
// gate said what that costs: "1 of 14 commands answer -h with a non-zero status
// … prbody (cobra without cliflags.Run: `-help` exits 2, and `-since`/`-public`
// are read as clusters of shorthands)". Asking a command what it does must not
// be an error, and a long flag spelled with one dash is how this fleet's
// commands are actually typed.
func run(argv []string, stdout, stderr io.Writer) int {
	cmd, codep := rootCmd()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if rc := cliflags.Run(cmd, argv); rc != 0 {
		return rc
	}
	return *codep
}

// rootCmd returns the command and a pointer to the status its run will set, so
// a verdict about the body is not confused with a usage error.
func rootCmd() (*cobra.Command, *int) {
	var repo string
	code := 0
	c := &cobra.Command{
		Use:   "prbody <body-file>",
		Short: "Whether every issue a pull-request body mentions says what happens to it",
		Long: `Reads a pull-request body and reports, for each issue it mentions, whether the
body says what happens to that issue — the same question ghmerge asks at merge
time, asked by the same code, before the pull request exists.

⛔ A bare ` + "`#N`" + ` means THIS repository. ` + "`Refs other/repo#89, #88`" + ` dispositions
other/repo#89 and this/repo#88 — not other/repo#88 — so a reference to another
repository has to be spelled in full after the keyword as well. That is what
refused a merge on 2026-11-09, and the fix is to say the repository twice.

It asks GitHub nothing: whether an issue is open is ghmerge's business, whether
a reference carries a disposition is text.

Exit status: 0 all dispositions stated, 1 at least one is bare, 2 unreadable.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.Flags().StringVar(&repo, "repo", "", "the pull request's own repository, for bare `#N` references")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		code = check(cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], repo)
		return nil
	}
	return c, &code
}

func check(stdout, stderr io.Writer, path, repo string) int {
	body, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stderr, "prbody:", err)
		return 2
	}
	// The repository stands in for a bare `#N`. Without one the parse still
	// works — the refs come back with an empty Repo — and the verdict is the
	// same, so this is a nicety for the message rather than a requirement.
	refs := issuegate.Parse(string(body), repo)
	if len(refs) == 0 {
		fmt.Fprintf(stdout, "%s mentions no issue\n", path)
		return 0
	}
	var bare []issuegate.Ref
	for _, r := range refs {
		fmt.Fprintf(stdout, "  %-7s %s\n", r.Kind, r)
		if r.Kind == issuegate.Bare {
			bare = append(bare, r)
		}
	}
	fmt.Fprintf(stdout, "%d reference(s)\n", len(refs))
	if len(bare) == 0 {
		return 0
	}
	fmt.Fprintf(stderr, "\nprbody: %d of %d reference(s) say nothing about what happens to them, "+
		"and ghmerge will refuse the merge for each:\n", len(bare), len(refs))
	for _, r := range bare {
		fmt.Fprintf(stderr, "  %s\n", r)
	}
	fmt.Fprintln(stderr, "\nWrite `Closes <ref>` if this pull request finishes it, or `Refs <ref>` if it "+
		"stays open on purpose. ⛔ A bare `#N` after the keyword means THIS repository: "+
		"`Refs other/repo#89, #88` dispositions other/repo#89 and this/repo#88, so spell the "+
		"repository on every reference that is not ours.")
	return 1
}
