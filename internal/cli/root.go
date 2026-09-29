// Package cli defines the WOMM command-line interface.
//
// Public contract (see CLI Contract note):
//
//	exit 0 — success
//	exit 1 — semantic FAIL (environment incompatibility)
//	exit 2 — usage error
//	exit 3 — execution/configuration error, or inconclusive
//	         verification (UNKNOWN/UNREACHABLE); wins over 1
//
// Commands: `version`, `capture`, `verify`. Exit 1 is produced by
// `verify` for a conclusive FAIL (see internal/verify.ExitCode).
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// version is the WOMM version printed by `womm version`. It is a
// variable so a release build can set it from the tag:
//
//	go build -ldflags "-X github.com/0xCHANDA/womm/internal/cli.version=0.1.0" ./cmd/womm
//
// Source builds report the development literal below.
var version = "0.0.1-dev"

// newRootCmd builds a fresh command tree. A new tree per execution
// keeps flag state from leaking between runs (cobra stores parsed
// flag values on the command), which matters for tests that drive
// the CLI repeatedly in one process.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "womm",
		Version: version,
		Short:   "Works On My Machine — development environment diagnostics",
		Long: "Works On My Machine (womm) is an evidence-based CLI that " +
			"discovers a project's declared requirements and verifies " +
			"whether this machine satisfies them.\n\n" +
			"v0.1 (Linux + Node.js): `capture` writes a project's declared " +
			"requirements to womm.yaml; `verify` checks this machine against " +
			"them and exits 0/1/2/3 (see `womm verify --help`).",
	}
	// Keep the public surface minimal: no generated completion
	// commands until the contract documents them.
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("womm version {{.Version}}\n")
	// One prefix for every failure line on stderr: cobra's own errors
	// and the diagnostics verify prints share the same stream.
	root.SetErrPrefix("error:")
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCaptureCmd())
	root.AddCommand(newVerifyCmd())
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
	// Ctrl-C / SIGTERM / SIGHUP (terminal or ssh drop) cancel the
	// context, which kills any running probe's whole process group
	// (see inspect/node). Without this a hung probe would outlive
	// WOMM: the probe runs in its own process group, so neither the
	// terminal's SIGINT nor its hangup reaches it directly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the CLI with explicit arguments and streams and returns
// the exit code instead of exiting, so tests can drive the real command
// tree end to end.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	// cobra prints usage on the *out* writer; keep stdout for the
	// commands' own output and show usage on stderr, only for usage
	// errors (execution errors are explained by their message).
	root.SilenceUsage = true
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	code := exitCodeFor(err)
	if code == 2 && cmd != nil {
		fmt.Fprintln(stderr, cmd.UsageString())
	}
	return code
}

// exitCodeFor maps cobra/pflag errors onto the public contract.
// Flag-parse failures and unknown commands are usage errors (2);
// anything else is an execution error (3).
func exitCodeFor(err error) int {
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return ec.code
	}
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
