package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/0xCHANDA/womm/internal/capture"
)

func newCaptureCmd() *cobra.Command {
	var (
		output string
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "capture [project-dir]",
		Short: "Write the project's declared requirements to womm.yaml",
		Long: "Reads the project's explicit requirement declarations " +
			"(package.json engines.node / packageManager, .nvmrc) and " +
			"writes them, with their evidence, to womm.yaml.\n\n" +
			"Only the project is read: the machine is never inspected and " +
			"no project code is executed. Ambiguous or conflicting " +
			"declarations are errors, never guesses.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			if output == "" {
				output = capture.DefaultOutput(root)
			}
			// Past this point every error is an execution error, not
			// a usage error: do not print the usage text for it.
			cmd.SilenceUsage = true

			file, err := capture.Capture(cmd.Context(), root)
			if err != nil {
				return err
			}
			if _, err := capture.Write(output, file, force); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d %s)\n",
				output, len(file.Requirements), plural(len(file.Requirements), "requirement"))
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "output file (default: <project-dir>/womm.yaml)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing output file")
	return cmd
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
