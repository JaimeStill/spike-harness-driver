package app

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
)

// App is the application: the command tree assembled over the infrastructure and the domain
// layer, and the output every command renders through.
type App struct {
	root *cobra.Command
	out  *output.Output
}

// New is the cold start: it composes the layers in dependency order and performs no I/O.
// cobra's own output (help, usage) goes through the root's writers, and every command's
// result through the one output.Output built over the same two streams.
func New(stdout, stderr io.Writer) *App {
	cfg := &Config{}
	out := output.New(stdout, stderr, func() bool { return cfg.All })
	infra := newInfrastructure(cfg)
	dom := newDomain(infra)
	scenarios := scenario.Scenarios(dom.Session, infra.Needs())

	root := newRoot(cfg, infra, scenarios)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(commands(dom, scenarios, out)...)

	return &App{root: root, out: out}
}

// Run is the hot start: it executes the tree under ctx, which cancels the running command
// when the process is signalled. The tree silences cobra's own reporting, so the error a
// command returns is rendered here, through the output's Error, and sets the exit code.
func (a *App) Run(ctx context.Context) int {
	if err := a.root.ExecuteContext(ctx); err != nil {
		a.out.Error(err)
		return 1
	}
	return 0
}

// SetArgs replaces the process arguments the tree parses, for tests.
func (a *App) SetArgs(args []string) { a.root.SetArgs(args) }

// newRoot builds the root command with cfg's persistent flags bound. Before any subcommand
// runs, it fails when the flags name a harness clutch has no adapter for or a --skills or
// --tools directory that doesn't load. Run without a subcommand, it prints its help and the
// scenario listing.
func newRoot(cfg *Config, infra *Infrastructure, scenarios []scenario.Scenario) *cobra.Command {
	root := &cobra.Command{
		Use:   "clutch",
		Short: "Drive an external agent harness from Go",
		Long: "clutch couples an external agent harness to Go: session opens harness sessions and\n" +
			"runs exchanges on them, printing every normalized event tagged with its session and\n" +
			"exchange IDs, and scenario runs narrated scenarios that each show one capability.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return infra.Validate()
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Scenarios:")
			scenario.WriteListing(cmd.OutOrStdout(), scenarios)
			return nil
		},
	}
	cfg.bind(root.PersistentFlags())
	return root
}
