package harness

import (
	"context"
	"sync"
	"time"
	"uuid"
)

// Exchange is one request-and-response block within a session. Its events pass through an
// EventQueue, so the session never waits on a slow consumer.
type Exchange struct {
	id         uuid.UUID
	sessionID  string
	cancelHook func()
	events     *EventQueue
	request    Request
	started    time.Time

	mu        sync.Mutex
	seq       int
	final     bool
	cancelled bool
	result    Result
	err       error
	unwatch   func() bool

	ended      chan struct{}
	cancelOnce sync.Once
}

func newExchange(sessionID string, session context.Context, req Request) *Exchange {
	return &Exchange{
		id:        uuid.NewV7(),
		sessionID: sessionID,
		events:    NewEventQueue(session),
		request:   req,
		started:   time.Now(),
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
	x.cancelOnce.Do(x.cancelHook)
}

// Wait blocks until the exchange ends and returns its result. The error is non-nil when the
// exchange ended in an error rather than a stop or a cancellation.
func (x *Exchange) Wait() (Result, error) {
	<-x.ended
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.result, x.err
}

func (x *Exchange) record(entries []string, err error) Record {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.err != nil {
		err = x.err
	}
	rec := Record{
		SessionID:  x.sessionID,
		ExchangeID: x.id,
		Request:    recorded(x.request),
		Result:     x.result,
		Entries:    entries,
		Started:    x.started,
		Ended:      time.Now(),
	}
	if err != nil {
		rec.Err = err.Error()
	}
	return rec
}

func (x *Exchange) watch(ctx context.Context) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.final {
		x.unwatch = context.AfterFunc(ctx, x.Cancel)
	}
}

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
			x.result.Usage.add(*ev.Usage)
		}
	case EventStructured:
		x.result.Structured = ev.Structured
	case EventCancelled:
		x.cancelled = true
	case EventError:
		if x.err == nil {
			x.err = ev.Err
		}
	case EventEnded:
		ev.StopReason = x.result.StopReason
		x.final = true
	}
	x.events.Push(ev)
	if ev.Kind == EventEnded {
		x.events.Close()
		close(x.ended)
		if x.unwatch != nil {
			x.unwatch()
		}
	}
}

func (x *Exchange) markCancelled() {
	x.mu.Lock()
	x.cancelled = true
	x.mu.Unlock()
}

func (x *Exchange) unanswered() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.request.Schema) > 0 && len(x.result.Structured) == 0 && !x.cancelled && x.err == nil
}

func (x *Exchange) fail(err error) {
	x.push(Event{Kind: EventError, Err: err})
	x.push(Event{Kind: EventEnded})
}

func recorded(req Request) Request {
	if len(req.Images) == 0 {
		return req
	}
	images := make([]Image, len(req.Images))
	for i, img := range req.Images {
		images[i] = Image{MediaType: img.MediaType}
	}
	req.Images = images
	return req
}
