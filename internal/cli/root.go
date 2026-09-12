// Package cli defines the WOMM command-line interface.
//
// Public contract (see CLI Contract note):
//
//	exit 0 — success
//	exit 1 — semantic FAIL (environment incompatibility)
//	exit 2 — usage error
//	exit 3 — execution/configuration error
//
// PR 1 wires only the root command and `version`. No exit code 1 is
// produced yet: semantic verification arrives in a later PR.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// version is the development version of WOMM.
const version = "0.0.1-dev"

var rootCmd = &cobra.Command{
	Use:   "womm",
	Short: "Works On My Machine — development environment diagnostics",
	Long: "Works On My Machine (womm) is an evidence-based CLI that " +
		"discovers project requirements, verifies local environments " +
		"and explains meaningful differences between machines.\n\n" +
		"Currently in early development: `version` is the only command.",
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the WOMM version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(cmd.OutOrStdout(), "womm version %s\n", version)
		return nil
	},
}

func init() {
	// Keep the public surface minimal for PR 1: no generated completion
	// commands until the contract documents them.
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.AddCommand(versionCmd)
}

// Execute runs the root command and maps errors to the public exit-code
// contract: usage errors exit 2, execution errors exit 3.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(exitCodeFor(err))
	}
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
	} {
		if strings.HasPrefix(msg, prefix) {
			return 2
		}
	}
	return 3
}
