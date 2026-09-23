package harness

import (
	"errors"
	"sync"
	"uuid"
)

// Exchange is one request-and-response block within a session. Its events pass through an
// EventQueue, so the session never waits on a slow consumer.
type Exchange struct {
	id        uuid.UUID
	sessionID string
	cancel    func() // the session's cancellation hook
	events    *EventQueue

	mu     sync.Mutex
	seq    int
	final  bool
	result Result
	err    error

	ended      chan struct{}
	cancelOnce sync.Once
}

// newExchange starts an exchange whose undelivered events are discarded when released closes.
func newExchange(sessionID string, released <-chan struct{}) *Exchange {
	return &Exchange{
		id:        uuid.NewV7(),
		sessionID: sessionID,
		events:    NewEventQueue(released),
		ended:     make(chan struct{}),
	}
}

// ID is the exchange ID the session assigned. IDs sort by creation time.
func (x *Exchange) ID() uuid.UUID { return x.id }

// Events streams the exchange's events in order. The channel closes after EventEnded, or when
// the session closes, which discards events not yet delivered.
func (x *Exchange) Events() <-chan Event { return x.events.Events() }

// Cancel asks the harness to stop the exchange and returns at once. The exchange still ends
// with EventCancelled and EventEnded. Cancelling an exchange that has ended does nothing.
func (x *Exchange) Cancel() {
	x.cancelOnce.Do(x.cancel)
}

// Wait blocks until the exchange ends and returns its result. The error is non-nil when the
// exchange ended in an error rather than a stop or a cancellation.
func (x *Exchange) Wait() (Result, error) {
	<-x.ended
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.result, x.err
}

// push stamps ev with the exchange's IDs and next sequence number, folds it into the result,
// and queues it. Events after EventEnded are dropped.
func (x *Exchange) push(ev Event) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.final {
		return
	}
	x.seq++
	ev.SessionID, ev.ExchangeID, ev.Seq = x.sessionID, x.id, x.seq
	switch ev.Kind {
	case EventMessageEnd:
		x.result.StopReason, x.result.Text = ev.StopReason, ev.Text
		if ev.Usage != nil {
			x.result.Usage = *ev.Usage
		}
	case EventError:
		if x.err == nil {
			x.err = errors.New(ev.Err)
		}
	case EventEnded:
		ev.StopReason = x.result.StopReason
		x.final = true
	}
	// Queued under x.mu, so events pushed from different goroutines keep Seq order.
	x.events.Push(ev)
	if ev.Kind == EventEnded {
		x.events.Close()
		close(x.ended)
	}
}

// fail ends the exchange with an error.
func (x *Exchange) fail(err error) {
	x.push(Event{Kind: EventError, Err: err.Error()})
	x.push(Event{Kind: EventEnded})
}
