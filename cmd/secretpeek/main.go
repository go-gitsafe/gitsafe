// Command secretpeek inspects a config or credential file by PROPERTIES: mode,
// size, format and its keys with every value replaced by a class
// (<secret jwt, 412 bytes>, <url https://host:port>, <string 12 bytes>). It is
// what to run instead of cat when the file may hold a credential; guard-read
// refuses the cat and names this tool.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/go-gitsafe/gitsafe/cliflags"
	"github.com/go-gitsafe/gitsafe/secretread"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	cmd := RootCmd()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cliflags.Run(cmd, args)
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "secretpeek [--key K] <file>",
		Short: "show a credential file's properties and keys, never a secret value",
		Long: `secretpeek prints a file's mode, size, format and its keys, each value replaced
by a class. With --key K it prints one value, only if K is a known non-secret
name (url, base_url, auth_url, host, username, region, project_id, ...) AND the
value has no secret shape.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if key != "" {
				v, err := secretread.Key(args[0], key)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), v)
				return nil
			}
			return secretread.Peek(cmd.OutOrStdout(), args[0])
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "print the value of this non-secret key")
	return cmd
}
