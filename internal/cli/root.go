// Package cli wires the whyx command-line interface onto the internal pipeline.
package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/jalet/whyx/internal/whyx"
)

// Execute runs the root command and sets the process exit code. Operating
// errors (bad target, unreadable repo, parse failure) exit non-zero; cobra has
// already printed them.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var cfg whyx.Config

	cmd := &cobra.Command{
		Use:   "whyx <target> <chart> [key]",
		Short: "Explain why a rendered Helm value is what it is",
		Long: "whyx replays the layered value-file merge for one (target, chart) " +
			"and shows, git-diff style, which layer set each value. Pass an " +
			"optional dotted key (e.g. image.tag) to trace just that value.",
		Args:          cobra.RangeArgs(2, 3),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Target = args[0]
			cfg.Chart = args[1]
			if len(args) == 3 {
				cfg.Key = args[2]
			}
			return whyx.Run(cmd.Context(), cfg, cmd.OutOrStdout())
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&cfg.RepoRoot, "repo", "", "path to the helm-charts repo (default: auto-detect)")
	flags.BoolVar(&cfg.Effective, "effective", false, "print only the final merged values")
	flags.BoolVar(&cfg.ListLayers, "layers", false, "list the resolved layer files in order")
	flags.StringVar(&cfg.Format, "format", "diff", "output format: diff|table|json")
	flags.BoolVar(&cfg.NoColor, "no-color", false, "disable colored output")
	flags.BoolVarP(&cfg.Verbose, "verbose", "v", false, "verbose diagnostics on stderr")

	return cmd
}
