package session

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/output"
)

// Commands builds the session command family over svc, rendering through out.
func Commands(svc *Service, out *output.Output) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Open harness sessions and run exchanges on them",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(sendCommand(svc, out))
	return cmd
}

// sendCommand builds "session send", which opens a session, runs one exchange, and closes it.
func sendCommand(svc *Service, out *output.Output) *cobra.Command {
	var cancelAfter int
	cmd := &cobra.Command{
		Use:   "send <prompt>",
		Short: "Open a session and run one exchange on it",
		Long: "send opens a session on the harness, sends the prompt as one exchange, prints every\n" +
			"normalized event tagged with its session and exchange IDs, and prints the result.\n" +
			"--cancel-after cancels the exchange after that many text deltas.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return send(cmd.Context(), svc, out, Exchange{Prompt: args[0], CancelAfter: cancelAfter})
		},
	}
	cmd.Flags().IntVar(&cancelAfter, "cancel-after", 0, "cancel after this many text deltas (0 never cancels)")
	return cmd
}

func send(ctx context.Context, svc *Service, out *output.Output, e Exchange) (err error) {
	sess, err := svc.Open(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sess.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", cerr))
		}
	}()
	out.Printf("session %s", sess.ID())
	o, err := svc.Run(ctx, sess, e, Observer{
		Sent:       func(id uuid.UUID) { out.Printf("exchange %s: %q", id, e.Prompt) },
		Event:      out.Event,
		Cancelling: func(n int) { out.Printf("-- cancelling after %d text deltas", n) },
	})
	if err != nil {
		return err
	}
	out.Result(o.Result, o.Err)
	return nil
}
