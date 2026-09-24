package scenario

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Command builds the cobra command that runs s, narrating through the reporter newReporter
// returns.
func Command(s Scenario, newReporter func() *Reporter) *cobra.Command {
	cmd := &cobra.Command{
		Use:   s.Name,
		Short: s.Summary,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Run(cmd.Context(), s, newReporter())
		},
	}
	if s.Flags != nil {
		s.Flags(cmd.Flags())
	}
	return cmd
}

// WriteListing writes each scenario's name, summary, and needs to w.
func WriteListing(w io.Writer, scenarios []Scenario) {
	for _, s := range scenarios {
		_, _ = fmt.Fprintf(w, "  %-10s %s\n", s.Name, s.Summary)
		for _, n := range s.Needs {
			_, _ = fmt.Fprintf(w, "  %-10s   needs %s\n", "", n.What)
		}
	}
}
