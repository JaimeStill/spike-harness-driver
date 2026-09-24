package scenario

import (
	"context"
	"fmt"

	"github.com/spf13/pflag"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
)

// cancelPrompt asks for a long answer, so the exchange is still streaming when it is
// cancelled.
const cancelPrompt = "Write a 600-word essay about the history of the Unix operating system."

// cancelScenario opens a session, cancels one exchange mid-stream, and closes the session.
func cancelScenario(svc *session.Service, needs []Need) Scenario {
	cancelAfter := 5
	return Scenario{
		Name:    "cancel",
		Summary: "One exchange cancelled mid-stream, ending as aborted",
		Needs:   needs,
		Flags: func(fs *pflag.FlagSet) {
			fs.IntVar(&cancelAfter, "cancel-after", cancelAfter, "text deltas before the exchange is cancelled")
		},
		Steps: func() ([]Step, func() error) {
			r := &run{svc: svc}
			return []Step{
				{Intent: "Open a session on the harness", Action: r.open},
				{
					Intent: fmt.Sprintf("Send a long prompt and cancel it after %d text deltas", cancelAfter),
					Action: func(ctx context.Context, rep *Reporter) error {
						o, err := r.exchange(ctx, rep, session.Exchange{Prompt: cancelPrompt, CancelAfter: cancelAfter})
						if err != nil {
							return err
						}
						if o.Result.StopReason != "aborted" {
							return fmt.Errorf("exchange ended with stop reason %q, want aborted", o.Result.StopReason)
						}
						return nil
					},
				},
				{
					Intent: "Close the session, which ends the harness process",
					Action: func(context.Context, *Reporter) error { return r.close() },
				},
			}, r.close
		},
	}
}
