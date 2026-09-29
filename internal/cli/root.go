// Package cli defines the WOMM command-line interface.
//
// Public contract (see CLI Contract note):
//
//	exit 0 — success
//	exit 1 — semantic FAIL (environment incompatibility)
//	exit 2 — usage error
//	exit 3 — execution/configuration error
//
// Commands: `version`, `capture`. No exit code 1 is produced yet:
// semantic verification arrives with `verify`.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// version is the development version of WOMM.
const version = "0.0.1-dev"

// newRootCmd builds a fresh command tree. A new tree per execution
// keeps flag state from leaking between runs (cobra stores parsed
// flag values on the command), which matters for tests that drive
// the CLI repeatedly in one process.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "womm",
		Short: "Works On My Machine — development environment diagnostics",
		Long: "Works On My Machine (womm) is an evidence-based CLI that " +
			"discovers project requirements, verifies local environments " +
			"and explains meaningful differences between machines.\n\n" +
			"Currently in early development: `capture` writes a project's " +
			"declared requirements to womm.yaml; `verify` is not available yet.",
	}
	// Keep the public surface minimal: no generated completion
	// commands until the contract documents them.
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCaptureCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the WOMM version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "womm version %s\n", version)
			return nil
		},
	}
}

// Execute runs the root command and maps errors to the public exit-code
// contract: usage errors exit 2, execution errors exit 3.
func Execute() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the CLI with explicit arguments and streams and returns
// the exit code instead of exiting, so tests can drive the real command
// tree end to end.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		return exitCodeFor(err)
	}
	return 0
}

// exitCodeFor maps cobra/pflag errors onto the public contract.
// Flag-parse failures and unknown commands are usage errors (2);
// anything else is an execution error (3).
func exitCodeFor(err error) int {
	msg := err.Error()
	for _, prefix := range []string{
		"unknown command",
		"unknown flag",
		"unknown shorthand",
		"unknown shorthand flag",
		"bad flag syntax",
		"flag needs an argument",
		"flag provided but not defined",
		"invalid argument",
		"no flag",
		// cobra positional-argument validators (cobra.MaximumNArgs,
		// ExactArgs, MinimumNArgs, RangeArgs).
		"accepts ",
		"requires at least",
	} {
		if strings.HasPrefix(msg, prefix) {
			return 2
		}
	}
	return 3
}
