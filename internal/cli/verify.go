package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/0xCHANDA/womm/internal/capture"
	"github.com/0xCHANDA/womm/internal/inspect"
	nodeinspect "github.com/0xCHANDA/womm/internal/inspect/node"
	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
	"github.com/0xCHANDA/womm/internal/report"
	"github.com/0xCHANDA/womm/internal/schema"
	"github.com/0xCHANDA/womm/internal/verify"
)

// newInspectors builds the inspector set `verify` uses, resolving
// executables through r. Production always probes the real machine
// through the hardened NodeInspector; same-package tests substitute
// fakes so results never depend on what the test host has installed.
var newInspectors = func(r *toolpath.Resolver) []inspect.Inspector {
	return []inspect.Inspector{nodeinspect.NewNodeInspectorWith(r)}
}

// accountHome returns the home directory the implicit version-manager
// shim directories hang off: the account's, from the user database —
// never the inherited $HOME, which a launcher can point into a project.
// A variable so tests never depend on the host's real home.
var accountHome = toolpath.AccountHome

// exitCodeError carries an exit code for outcomes that are not
// errors in the usual sense (a FAIL verdict, an inconclusive run). The
// command has already written everything the user needs; cobra must
// neither print it nor show usage.
type exitCodeError struct{ code int }

func (e *exitCodeError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// diagnostics converts operational errors into the report's neutral
// form, classified by sentinel (never by text).
func diagnostics(errs []error) []report.Diagnostic {
	out := make([]report.Diagnostic, 0, len(errs))
	for _, e := range errs {
		out = append(out, report.Diagnostic{Kind: verify.ErrorKind(e), Message: e.Error()})
	}
	return out
}

// Output formats of `verify`.
const (
	formatHuman = "human"
	formatJSON  = "json"
)

func newVerifyCmd() *cobra.Command {
	var file string
	var toolDirs []string
	var format string
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
			"failure) — 3 takes precedence over 1.\n\n" +
			"Executables are resolved from --tool-dir directories (in the " +
			"order given), then /usr/local/bin, /usr/bin and /bin, then the " +
			"account's ~/.volta/bin, ~/.asdf/shims, ~/.local/share/mise/shims " +
			"and ~/.local/bin when they exist and are safe — never from the " +
			"inherited PATH. A --tool-dir inside the project, under a " +
			"node_modules or world-writable is refused; one owned by another " +
			"user is accepted with a warning. An unsafe account directory " +
			"is skipped with a warning.\n\n" +
			"--format json prints one JSON document (schema version 1, " +
			"docs/output-json.md) on stdout instead of the report; exit " +
			"status and stderr are identical in both formats.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != formatHuman && format != formatJSON {
				// A bad flag value is a usage error (exit 2), before anything runs.
				return fmt.Errorf("invalid argument %q for \"--format\" flag: must be %q or %q", format, formatHuman, formatJSON)
			}
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			if file == "" {
				if fi, err := os.Stat(root); err == nil && !fi.IsDir() {
					// `womm verify path/to/womm.yaml`: the argument is
					// the project directory; the file goes in -f.
					return fmt.Errorf("%s is a file, not a project directory; use --file %s (or -f) to verify a specific womm.yaml", root, root)
				}
				file = capture.DefaultOutput(root)
			}
			resolver, err := toolpath.New(toolpath.Config{
				// Whatever lives in the project (or beside the file being
				// verified) is project-controlled and never searched.
				ProjectRoots: []string{root, filepath.Dir(file)},
				ExplicitDirs: toolDirs,
				UserHome:     accountHome(),
			})
			if err != nil {
				var de *toolpath.DirError
				if errors.As(err, &de) {
					// A bad flag value is a usage error (exit 2).
					return fmt.Errorf("invalid argument %q for \"--tool-dir\" flag: %s", de.Dir, de.Reason)
				}
				return err
			}
			for _, w := range resolver.Warnings() {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			f, warnings, err := schema.Load(file)
			for _, w := range warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("no womm.yaml at %s; run `womm capture %s` to create one, or pass --file", file, root)
			}
			if err != nil {
				return err
			}

			res := verify.Verify(cmd.Context(), f, newInspectors(resolver))
			if verify.Cancelled(res) {
				// An interrupted run has no verdict to show: partial
				// PASS lines would read as a result. Say what
				// happened and exit inconclusive.
				fmt.Fprintln(cmd.ErrOrStderr(), "error: verification cancelled; no result")
				cmd.SilenceErrors = true
				return &exitCodeError{code: verify.ExitInconclusive}
			}
			var renderErr error
			switch format {
			case formatJSON:
				renderErr = report.RenderJSON(cmd.OutOrStdout(), report.JSONDocument{
					Result:      verify.Verdict(res),
					ExitCode:    verify.ExitCode(res),
					Matches:     res.Matches,
					Diagnostics: diagnostics(res.Errors),
				})
			default:
				renderErr = report.Render(cmd.OutOrStdout(), res.Matches)
			}
			if renderErr != nil {
				return renderErr
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
	cmd.Flags().StringVar(&format, "format", formatHuman, "output format: \"human\" or \"json\" (one JSON document on stdout; see docs/output-json.md)")
	cmd.Flags().StringArrayVar(&toolDirs, "tool-dir", nil, "absolute directory to resolve tools from, before the system directories (repeatable; e.g. a version manager's bin directory)")
	return cmd
}
