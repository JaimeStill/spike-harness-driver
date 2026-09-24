package scenario

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Scenarios returns every scenario in presentation order, running exchanges through svc. Each
// scenario checks needs, which say what the harness requires to run.
func Scenarios(svc *session.Service, needs []Need) []Scenario {
	return []Scenario{
		exchangeScenario(svc, needs),
		cancelScenario(svc, needs),
		resumeScenario(svc, needs),
		toolScenario(svc, needs),
		skillScenario(svc, needs),
	}
}

// run is the state one scenario run shares between its steps.
type run struct {
	svc  *session.Service
	sess *harness.Session
	// events are the events of the run's last exchange.
	events []harness.Event
}

// open opens a new session for the run and notes its ID.
func (r *run) open(ctx context.Context, rep *Reporter) error {
	return r.resume(ctx, rep, "")
}

// resume opens the session id names for the run, a new one when id is empty, and notes its
// ID.
func (r *run) resume(ctx context.Context, rep *Reporter, id string) error {
	return r.openWith(ctx, rep, id, session.Setup{})
}

// openWith opens the session id names for the run as resume does, offering setup's tools and
// skills.
func (r *run) openWith(ctx context.Context, rep *Reporter, id string, setup session.Setup) error {
	sess, err := r.svc.OpenWith(ctx, id, setup)
	if err != nil {
		return err
	}
	r.sess = sess
	rep.Note("session %s", sess.ID())
	return nil
}

// exchange runs e on the run's session, narrating its events and result, and keeps its events.
func (r *run) exchange(ctx context.Context, rep *Reporter, e session.Exchange) (session.Outcome, error) {
	if r.sess == nil {
		return session.Outcome{}, errors.New("no open session")
	}
	r.events = nil
	obs := observer(rep, e.Prompt)
	obs.Event = func(ev harness.Event) {
		r.events = append(r.events, ev)
		rep.Event(ev)
	}
	o, err := r.svc.Run(ctx, r.sess, e, obs)
	if err != nil {
		return o, err
	}
	rep.Result(o.Result, o.Err)
	return o, nil
}

// ended fails when the exchange wasn't accepted, or ended in an error rather than a stop or a
// cancellation.
func ended(o session.Outcome, err error) error {
	if err != nil {
		return err
	}
	if o.Err != nil {
		return fmt.Errorf("exchange ended in error: %w", o.Err)
	}
	return nil
}

// toolRan fails unless the run's last exchange both called the tool name names and received
// its result.
func (r *run) toolRan(name string) error {
	var called, returned bool
	for _, ev := range r.events {
		if ev.Tool == nil || ev.Tool.Name != name {
			continue
		}
		called = called || ev.Kind == harness.EventToolCall
		returned = returned || ev.Kind == harness.EventToolResult
	}
	switch {
	case !called:
		return fmt.Errorf("the model never called the %s tool", name)
	case !returned:
		return fmt.Errorf("the %s tool was called but no result came back", name)
	}
	return nil
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
