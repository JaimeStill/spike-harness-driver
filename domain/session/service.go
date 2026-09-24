package session

import (
	"context"
	"time"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// openTimeout bounds a harness's start-up handshake.
const openTimeout = 30 * time.Second

// Service runs exchanges on one harness.
type Service struct {
	driver  func() (harness.Driver, error)
	options func() harness.Options
}

// New returns a Service over the driver and session options the two functions resolve. Both
// are called at each Open, so they can follow flags parsed after the Service is built.
func New(driver func() (harness.Driver, error), options func() harness.Options) *Service {
	return &Service{driver: driver, options: options}
}

// Exchange describes one exchange to run.
type Exchange struct {
	Prompt string
	// CancelAfter cancels the exchange after that many text deltas. Zero runs it to its end.
	CancelAfter int
}

// Observer receives what an exchange does as it happens. A nil field ignores it.
type Observer struct {
	// Sent is called once the harness accepts the prompt, with the exchange's ID.
	Sent  func(exchangeID uuid.UUID)
	Event func(harness.Event)
	// Cancelling is called when the exchange is cancelled, with the text deltas seen so far.
	Cancelling func(deltas int)
}

// Outcome is how an exchange ended.
type Outcome struct {
	ExchangeID uuid.UUID
	Result     harness.Result
	// Err is the error the exchange ended in, if any, rather than a stop or a cancellation.
	Err error
}

// Open opens a session on the harness. The start-up handshake is bounded by ctx and by
// openTimeout; the session lives until the caller closes it.
func (s *Service) Open(ctx context.Context) (*harness.Session, error) {
	d, err := s.driver()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	return d.Open(ctx, s.options())
}

// Run sends e on sess and follows it to its end. The error is non-nil only when the harness
// didn't accept the prompt; how the exchange itself ended is in the Outcome.
func (s *Service) Run(ctx context.Context, sess *harness.Session, e Exchange, obs Observer) (Outcome, error) {
	x, err := sess.Send(ctx, harness.Request{Text: e.Prompt})
	if err != nil {
		return Outcome{}, err
	}
	if obs.Sent != nil {
		obs.Sent(x.ID())
	}
	deltas := 0
	for ev := range x.Events() {
		if obs.Event != nil {
			obs.Event(ev)
		}
		if ev.Kind != harness.EventTextDelta {
			continue
		}
		if deltas++; deltas == e.CancelAfter {
			if obs.Cancelling != nil {
				obs.Cancelling(deltas)
			}
			x.Cancel()
		}
	}
	res, err := x.Wait()
	return Outcome{ExchangeID: x.ID(), Result: res, Err: err}, nil
}
