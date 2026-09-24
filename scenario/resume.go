package scenario

import (
	"context"
	"fmt"
	"slices"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

// resumePrompts are the two exchanges: the second can only be answered from the first.
var resumePrompts = [2]string{
	"Reply with one short sentence: what is Go?",
	"What was my first question? Answer in one short sentence.",
}

// resumeScenario runs an exchange, ends the harness process, resumes the session by ID in a
// new process, and runs a second exchange that depends on the first.
func resumeScenario(svc *session.Service, needs []Need) Scenario {
	return Scenario{
		Name:    "resume",
		Summary: "A session resumed by ID in a new harness process, keeping its history and exchange IDs",
		Needs:   needs,
		Steps: func() ([]Step, func() error) {
			r := &run{svc: svc}
			var (
				id    string
				first uuid.UUID
			)
			return []Step{
				{Intent: "Open a new session on the harness", Action: func(ctx context.Context, rep *Reporter) error {
					if err := r.open(ctx, rep); err != nil {
						return err
					}
					id = r.sess.ID()
					return nil
				}},
				{
					Intent: "Run the first exchange to its end",
					Action: func(ctx context.Context, rep *Reporter) error {
						o, err := r.exchange(ctx, rep, session.Exchange{Prompt: resumePrompts[0]})
						first = o.ExchangeID
						return ended(o, err)
					},
				},
				{
					Intent: "Close the session, which ends the harness process",
					Action: func(context.Context, *Reporter) error { return r.close() },
				},
				{
					Intent: "Resume the session by ID in a new harness process, checking its records against the harness",
					Action: func(ctx context.Context, rep *Reporter) error {
						if err := r.resume(ctx, rep, id); err != nil {
							return err
						}
						return r.listExchanges(ctx, rep, first)
					},
				},
				{
					Intent: "Ask a question only the session's history can answer",
					Action: func(ctx context.Context, rep *Reporter) error {
						return ended(r.exchange(ctx, rep, session.Exchange{Prompt: resumePrompts[1]}))
					},
				},
				{
					Intent: "List the session's exchanges: both IDs, each bound to its own harness entries",
					Action: func(ctx context.Context, rep *Reporter) error {
						return r.listExchanges(ctx, rep, first)
					},
				},
			}, r.close
		},
	}
}

// listExchanges notes the run's recorded exchanges, and fails unless want is among them.
func (r *run) listExchanges(ctx context.Context, rep *Reporter, want uuid.UUID) error {
	recs, err := r.sess.Exchanges(ctx)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		rep.Record(rec)
	}
	if !slices.ContainsFunc(recs, func(rec harness.Record) bool { return rec.ExchangeID == want }) {
		return fmt.Errorf("exchange %s is not among the session's %d records", want, len(recs))
	}
	return nil
}
