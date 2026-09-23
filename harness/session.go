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
type Session struct {
	id         string
	connection Connection

	mu      sync.Mutex
	open    *Exchange
	exited  bool
	exitErr error
	closing bool

	done      chan struct{} // closed when the Connection's events have closed
	released  chan struct{} // closed by Close; releases undrained exchanges
	closeOnce sync.Once
	closeErr  error
}

// NewSession starts a session over c. id is the harness's own session ID.
func NewSession(id string, c Connection) *Session {
	s := &Session{
		id:         id,
		connection: c,
		done:       make(chan struct{}),
		released:   make(chan struct{}),
	}
	go s.route()
	return s
}

// ID is the harness's session ID.
func (s *Session) ID() string { return s.id }

// Send opens an exchange and prompts the harness with req. Cancelling ctx cancels the
// exchange. Send returns ErrBusy while another exchange is open, ErrClosed after Close, and
// the harness's exit error after the harness has exited.
func (s *Session) Send(ctx context.Context, req Request) (*Exchange, error) {
	s.mu.Lock()
	switch {
	case s.closing:
		s.mu.Unlock()
		return nil, ErrClosed
	case s.exited:
		s.mu.Unlock()
		return nil, s.exitErr
	case s.open != nil:
		s.mu.Unlock()
		return nil, ErrBusy
	}
	x := newExchange(s.id, s.released)
	x.cancel = func() { s.cancel(x) }
	s.open = x
	s.mu.Unlock()

	if err := s.connection.Prompt(ctx, req); err != nil {
		s.mu.Lock()
		if s.open == x {
			s.open = nil
		}
		s.mu.Unlock()
		x.fail(err)
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			x.Cancel()
		case <-x.ended:
		}
	}()
	return x, nil
}

// Close ends the harness. Events an exchange hasn't delivered yet are discarded.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		s.closeErr = s.connection.Close()
		<-s.done
		close(s.released)
	})
	return s.closeErr
}

// cancel is every exchange's cancellation hook. It cancels only while x is open: once x has
// ended, a cancellation would stop the next exchange's run instead.
func (s *Session) cancel(x *Exchange) {
	s.mu.Lock()
	open := s.open == x
	s.mu.Unlock()
	if !open {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
		defer cancel()
		if err := s.connection.Cancel(ctx); err != nil {
			x.push(Event{Kind: EventError, Err: err.Error()})
		}
	}()
}

// route hands each of the Connection's events to the open exchange, and ends that exchange when the
// Connection's events close. An event outside any exchange, such as a harness's queue notice after
// a cancelled run ends, is dropped.
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
