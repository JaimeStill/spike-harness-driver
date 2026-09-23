package harness

import (
	"context"
	"errors"
	"sync"
	"time"
)

// cancelTimeout bounds the Connection.Cancel call a cancellation makes.
const cancelTimeout = 30 * time.Second

// Session is one conversation with a harness that spans several exchanges. It carries one
// exchange at a time and routes the Connection's events to it, from Send to EventEnded.
//
// The session's lifetime is a context it owns, which Close cancels with ErrClosed. That
// releases the exchanges' undelivered events and stops any cancellation still in flight.
type Session struct {
	id         string
	connection Connection
	ctx        context.Context
	close      context.CancelCauseFunc

	mu      sync.Mutex
	open    *Exchange
	exited  bool
	exitErr error
	// cancelling is closed when the cancellation in flight finishes; nil when none is.
	cancelling chan struct{}

	done      chan struct{} // closed when the Connection's events have closed
	closeOnce sync.Once
	closeErr  error
}

// NewSession starts a session over c. id is the harness's own session ID. Like a network
// connection, the session takes no context: it lives until Close, or until the harness exits.
func NewSession(id string, c Connection) *Session {
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &Session{
		id:         id,
		connection: c,
		ctx:        ctx,
		close:      cancel,
		done:       make(chan struct{}),
	}
	go s.route()
	return s
}

// ID is the harness's session ID.
func (s *Session) ID() string { return s.id }

// Send opens an exchange and prompts the harness with req. Cancelling ctx cancels the
// exchange. Send returns ErrBusy while another exchange is open, ErrClosed after Close, and
// the harness's exit error after the harness has exited.
//
// Send returns once the harness has accepted the prompt, however ctx ends in the meantime: a
// prompt already written may be accepted, and only an accepted run can be cancelled. If ctx
// has ended by then, the exchange is cancelled at once. Send also waits, for as long as ctx
// allows, for a cancellation of the previous exchange to finish, so that cancellation can't
// reach this exchange's run.
func (s *Session) Send(ctx context.Context, req Request) (*Exchange, error) {
	s.mu.Lock()
	for s.cancelling != nil {
		pending := s.cancelling
		s.mu.Unlock()
		select {
		case <-pending:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
	}
	switch {
	case s.ctx.Err() != nil:
		s.mu.Unlock()
		return nil, context.Cause(s.ctx)
	case s.exited:
		s.mu.Unlock()
		return nil, s.exitErr
	case s.open != nil:
		s.mu.Unlock()
		return nil, ErrBusy
	}
	x := newExchange(s.id, s.ctx)
	x.cancel = func() { s.cancel(x) }
	s.open = x
	s.mu.Unlock()

	if err := s.connection.Prompt(s.ctx, req); err != nil {
		if s.ctx.Err() != nil {
			err = context.Cause(s.ctx)
		}
		s.mu.Lock()
		if s.open == x {
			s.open = nil
		}
		s.mu.Unlock()
		x.fail(err)
		return nil, err
	}
	x.watch(ctx)
	return x, nil
}

// Close ends the harness. Events an exchange hasn't delivered yet are discarded.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.close(ErrClosed)
		s.closeErr = s.connection.Close()
		<-s.done
	})
	return s.closeErr
}

// cancel is every exchange's cancellation hook. It cancels only while x is open: once x has
// ended, a cancellation would stop the next exchange's run instead. A harness may take several
// round trips to cancel, and x may end during them, so the cancellation holds off the next
// Send until it finishes.
func (s *Session) cancel(x *Exchange) {
	s.mu.Lock()
	if s.open != x {
		s.mu.Unlock()
		return
	}
	done := make(chan struct{})
	s.cancelling = done
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			s.cancelling = nil
			s.mu.Unlock()
			close(done)
		}()
		ctx, cancel := context.WithTimeout(s.ctx, cancelTimeout)
		defer cancel()
		// A cancellation that Close cut short is no error of the exchange's.
		if err := s.connection.Cancel(ctx); err != nil && s.ctx.Err() == nil {
			x.push(Event{Kind: EventError, Err: err.Error()})
		}
	}()
}

// route hands each of the Connection's events to the open exchange, and ends that exchange
// when the Connection's events close. An event outside any exchange, such as a harness's queue
// notice after a cancelled run ends, is dropped.
func (s *Session) route() {
	var last Event
	for ev := range s.connection.Events() {
		last = ev
		s.mu.Lock()
		x := s.open
		if ev.Kind == EventEnded {
			s.open = nil
		}
		s.mu.Unlock()
		if x != nil {
			x.push(ev)
		}
	}

	exitErr := ErrClosed
	if last.Kind == EventError {
		exitErr = errors.New(last.Err)
	}
	s.mu.Lock()
	s.exited = true
	s.exitErr = exitErr
	x := s.open
	s.open = nil
	s.mu.Unlock()
	switch {
	case x == nil:
	case last.Kind == EventError:
		// The exit error has already reached x.
		x.push(Event{Kind: EventEnded})
	default:
		x.fail(exitErr)
	}
	close(s.done)
}
