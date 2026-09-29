package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/0xCHANDA/womm/internal/capture"
	"github.com/0xCHANDA/womm/internal/inspect"
	nodeinspect "github.com/0xCHANDA/womm/internal/inspect/node"
	"github.com/0xCHANDA/womm/internal/report"
	"github.com/0xCHANDA/womm/internal/schema"
	"github.com/0xCHANDA/womm/internal/verify"
)

// newInspectors builds the inspector set `verify` uses. Production
// always probes the real machine through the hardened NodeInspector;
// same-package tests substitute fakes so results never depend on what
// the test host has installed.
var newInspectors = func() []inspect.Inspector {
	return []inspect.Inspector{nodeinspect.NewNodeInspector()}
}

// exitCodeError carries an exit code for outcomes that are not
// errors in the usual sense (a FAIL verdict, an inconclusive run). The
// command has already written everything the user needs; cobra must
// neither print it nor show usage.
type exitCodeError struct{ code int }

func (e *exitCodeError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func newVerifyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "verify [project-dir]",
		Short: "Check this machine against the project's womm.yaml",
		Long: "Loads womm.yaml, observes each required tool on this machine " +
			"(fixed --version probes of system binaries only; no project code " +
			"is ever executed) and reports whether the observation satisfies " +
			"each requirement.\n\n" +
			"Exit status: 0 all PASS; 1 at least one FAIL and nothing " +
			"inconclusive; 2 usage error; 3 inconclusive (UNKNOWN or " +
			"UNREACHABLE result, malformed configuration, or an operational " +
			"failure) — 3 takes precedence over 1.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			if file == "" {
				file = capture.DefaultOutput(root)
			}
			f, warnings, err := schema.Load(file)
			for _, w := range warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			if err != nil {
				return err
			}

			res := verify.Verify(cmd.Context(), f, newInspectors())
			if err := report.Render(cmd.OutOrStdout(), res.Matches); err != nil {
				return err
			}
			for _, e := range res.Errors {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", e)
			}
			code := verify.ExitCode(res)
			if code == verify.ExitPass {
				return nil
			}
			// Everything has been reported already; cobra must add
			// nothing.
			cmd.SilenceErrors = true
			return &exitCodeError{code: code}
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "womm.yaml to verify (default: <project-dir>/womm.yaml)")
	return cmd
}
