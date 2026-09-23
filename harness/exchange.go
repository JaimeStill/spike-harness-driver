package harness

import (
	"errors"
	"sync"
	"uuid"
)

// Exchange is one request-and-response block within a session. Its events queue without bound
// and a pump delivers them in order, so the session never waits on a slow consumer.
type Exchange struct {
	id        uuid.UUID
	sessionID string
	cancel    func()          // the session's cancellation hook
	released  <-chan struct{} // closed when the session closes

	mu     sync.Mutex
	queue  []Event
	seq    int
	final  bool
	result Result
	err    error

	wake       chan struct{}
	events     chan Event
	ended      chan struct{}
	cancelOnce sync.Once
}

func newExchange(sessionID string, released <-chan struct{}) *Exchange {
	return &Exchange{
		id:        uuid.NewV7(),
		sessionID: sessionID,
		released:  released,
		wake:      make(chan struct{}, 1),
		events:    make(chan Event),
		ended:     make(chan struct{}),
	}
}

// ID is the exchange ID the session assigned. IDs sort by creation time.
func (x *Exchange) ID() uuid.UUID { return x.id }

// Events streams the exchange's events in order. The channel closes after EventEnded, or when
// the session closes, which discards events not yet delivered.
func (x *Exchange) Events() <-chan Event { return x.events }

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
	if x.final {
		x.mu.Unlock()
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
	x.queue = append(x.queue, ev)
	x.mu.Unlock()

	if ev.Kind == EventEnded {
		close(x.ended)
	}
	select {
	case x.wake <- struct{}{}:
	default:
	}
}

// fail ends the exchange with an error.
func (x *Exchange) fail(err error) {
	x.push(Event{Kind: EventError, Err: err.Error()})
	x.push(Event{Kind: EventEnded})
}

// pump delivers queued events until it delivers EventEnded or the session closes.
func (x *Exchange) pump() {
	defer close(x.events)
	for {
		x.mu.Lock()
		batch := x.queue
		x.queue = nil
		x.mu.Unlock()
		for _, ev := range batch {
			select {
			case x.events <- ev:
			case <-x.released:
				return
			}
			if ev.Kind == EventEnded {
				return
			}
		}
		if len(batch) == 0 {
			select {
			case <-x.wake:
			case <-x.released:
				return
			}
		}
	}
}
