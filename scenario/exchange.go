package scenario

import (
	"context"
	"fmt"

	"github.com/JaimeStill/spike-harness-driver/domain/session"
)

// exchangePrompt asks for a short answer, so the exchange ends on its own quickly.
const exchangePrompt = "Reply with one short sentence: what is Go?"

// exchangeScenario opens a session, runs one exchange to its end, and closes the session.
func exchangeScenario(svc *session.Service, needs []Need) Scenario {
	return Scenario{
		Name:    "exchange",
		Summary: "One exchange run to its end, every event tagged with its session and exchange IDs",
		Needs:   needs,
		Steps: func() ([]Step, func() error) {
			r := &run{svc: svc}
			return []Step{
				{Intent: "Open a session on the harness", Action: r.open},
				{
					Intent: "Send one prompt and stream the exchange until the harness ends it",
					Action: func(ctx context.Context, rep *Reporter) error {
						o, err := r.exchange(ctx, rep, session.Exchange{Prompt: exchangePrompt})
						if err != nil {
							return err
						}
						if o.Err != nil {
							return fmt.Errorf("exchange ended in error: %w", o.Err)
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
