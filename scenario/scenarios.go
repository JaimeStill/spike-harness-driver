package scenario

import (
	"context"
	"errors"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Scenarios returns every scenario in presentation order, running exchanges through svc. Each
// scenario checks needs, which say what the harness requires to run.
func Scenarios(svc *session.Service, needs []Need) []Scenario {
	return []Scenario{
		exchangeScenario(svc, needs),
		cancelScenario(svc, needs),
	}
}

// run is the state one scenario run shares between its steps.
type run struct {
	svc  *session.Service
	sess *harness.Session
}

// open opens the run's session and notes its ID.
func (r *run) open(ctx context.Context, rep *Reporter) error {
	sess, err := r.svc.Open(ctx)
	if err != nil {
		return err
	}
	r.sess = sess
	rep.Note("session %s", sess.ID())
	return nil
}

// exchange runs e on the run's session, narrating its events and result.
func (r *run) exchange(ctx context.Context, rep *Reporter, e session.Exchange) (session.Outcome, error) {
	if r.sess == nil {
		return session.Outcome{}, errors.New("no open session")
	}
	o, err := r.svc.Run(ctx, r.sess, e, observer(rep, e.Prompt))
	if err != nil {
		return o, err
	}
	rep.Result(o.Result, o.Err)
	return o, nil
}

// close closes the run's session, if one is open.
func (r *run) close() error {
	if r.sess == nil {
		return nil
	}
	s := r.sess
	r.sess = nil
	return s.Close()
}

func observer(rep *Reporter, prompt string) session.Observer {
	return session.Observer{
		Sent:       func(id uuid.UUID) { rep.Note("exchange %s: %q", id, prompt) },
		Event:      rep.Event,
		Cancelling: rep.Cancelling,
	}
}
