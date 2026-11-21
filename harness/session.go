package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const cancelTimeout = 30 * time.Second

const recordTimeout = 30 * time.Second

// Session is one conversation with a harness that spans several exchanges. It carries one
// exchange at a time and routes the Connection's events to it, from Send to EventEnded.
//
// The session's lifetime is a context it owns, which Close cancels with ErrClosed. That
// releases the exchanges' undelivered events and stops any cancellation still in flight.
//
// With a Store, the session records each exchange as it ends, before the exchange's
// EventEnded is delivered and before the next Send can open another. When the Connection
// keeps a Journal, the record binds the exchange to the harness entries it appended: the
// entries after the session's cursor, which then advances to the last of them. When binding
// fails, the exchange is recorded without entries, and the next Send takes the cursor from
// the journal's head again, so no later exchange claims the unbound entries.
type Session struct {
	id         string
	connection Connection
	store      Store
	journal    Journal
	ctx        context.Context
	close      context.CancelCauseFunc

	mu         sync.Mutex
	open       *Exchange
	runEnded   bool
	cursor     string
	resync     bool
	exited     bool
	exitErr    error
	cancelling chan struct{}

	eventsClosed chan struct{}
	closeOnce    sync.Once
	closeErr     error
}

// NewSession starts a session over c, recording its exchanges in store, which may be nil. id
// is the harness's own session ID. ctx bounds only the setup: like a network connection, the
// session lives until Close, or until the harness exits.
//
// When there is a store and c keeps a Journal, setup takes the journal's head as the cursor,
// and checks that the last entry the store has recorded for id is still in the journal. If it
// isn't, NewSession fails with ErrJournalMismatch. NewSession doesn't close c when it fails.
func NewSession(ctx context.Context, id string, c Connection, store Store) (*Session, error) {
	s := &Session{id: id, connection: c, store: store, eventsClosed: make(chan struct{})}
	if j, ok := c.(Journal); ok && store != nil {
		s.journal = j
		head, err := j.Head(ctx)
		if err != nil {
			return nil, fmt.Errorf("harness: journal head: %w", err)
		}
		s.cursor = head
		if err := s.verify(ctx); err != nil {
			return nil, err
		}
	}
	s.ctx, s.close = context.WithCancelCause(context.Background())
	go s.route()
	return s, nil
}

func (s *Session) verify(ctx context.Context) error {
	recs, err := s.store.Records(ctx, s.id)
	if err != nil {
		return fmt.Errorf("harness: records of session %s: %w", s.id, err)
	}
	last := ""
	for _, r := range recs {
		if len(r.Entries) > 0 {
			last = r.Entries[len(r.Entries)-1]
		}
	}
	if last == "" {
		return nil
	}
	if _, err := s.journal.Since(ctx, last); err != nil {
		if errors.Is(err, ErrUnknownEntry) {
			return fmt.Errorf("%w: session %s, entry %s", ErrJournalMismatch, s.id, last)
		}
		return fmt.Errorf("harness: journal: %w", err)
	}
	return nil
}

// ID is the harness's session ID.
func (s *Session) ID() string { return s.id }

// Journaled reports whether the session binds its exchanges to the harness's journal, which
// needs a Store and a Connection that keeps a Journal. Only a journaled session's records were
// checked against the harness when it opened.
func (s *Session) Journaled() bool { return s.journal != nil }

// Send opens an exchange and prompts the harness with req. Cancelling ctx cancels the
// exchange. Send returns ErrInvalidSchema, wrapped, for a request whose schema isn't an object
// schema, ErrInvalidImage, wrapped, for an image without a media type or data, ErrBusy while
// another exchange is open, ErrClosed after Close, and the harness's exit error after the
// harness has exited.
//
// Send returns once the harness has accepted the prompt, however ctx ends in the meantime: a
// prompt already written may be accepted, and only an accepted run can be cancelled. If ctx
// has ended by then, the exchange is cancelled at once. Send also waits, for as long as ctx
// allows, for a cancellation of the previous exchange to finish, so that cancellation can't
// reach this exchange's run.
func (s *Session) Send(ctx context.Context, req Request) (*Exchange, error) {
	if len(req.Schema) > 0 {
		if err := ValidateSchema(req.Schema); err != nil {
			return nil, err
		}
	}
	if err := validateImages(req.Images); err != nil {
		return nil, err
	}
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
	x := newExchange(s.id, s.ctx, req)
	x.cancelHook = func() { s.cancel(x) }
	s.open = x
	resync := s.resync
	s.mu.Unlock()

	err := s.resyncCursor(resync)
	if err == nil {
		err = s.connection.Prompt(s.ctx, req)
	}
	if err != nil {
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

// Exchanges returns the session's recorded exchanges in the order they ended, including those
// recorded before the session was resumed. It returns none without a Store.
func (s *Session) Exchanges(ctx context.Context) ([]Record, error) {
	if s.store == nil {
		return nil, nil
	}
	return s.store.Records(ctx, s.id)
}

// Close ends the harness. Events an exchange hasn't delivered yet are discarded.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.close(ErrClosed)
		s.closeErr = s.connection.Close()
		<-s.eventsClosed
	})
	return s.closeErr
}

func (s *Session) resyncCursor(resync bool) error {
	if !resync {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, recordTimeout)
	defer cancel()
	head, err := s.journal.Head(ctx)
	if err != nil {
		return fmt.Errorf("harness: journal head: %w", err)
	}
	s.mu.Lock()
	s.cursor, s.resync = head, false
	s.mu.Unlock()
	return nil
}

func (s *Session) cancel(x *Exchange) {
	s.mu.Lock()
	if s.open != x || s.runEnded {
		s.mu.Unlock()
		return
	}
	done := make(chan struct{})
	s.cancelling = done
	s.mu.Unlock()
	x.markCancelled()
	go func() {
		defer func() {
			s.mu.Lock()
			s.cancelling = nil
			s.mu.Unlock()
			close(done)
		}()
		ctx, cancel := context.WithTimeout(s.ctx, cancelTimeout)
		defer cancel()
		if err := s.connection.Cancel(ctx); err != nil && s.ctx.Err() == nil {
			x.push(Event{Kind: EventError, Err: err})
		}
	}()
}

func (s *Session) route() {
	for ev := range s.connection.Events() {
		s.mu.Lock()
		x := s.open
		if x != nil && ev.Kind == EventEnded {
			s.runEnded = true
		}
		s.mu.Unlock()
		if x != nil && ev.Kind == EventEnded {
			if x.unanswered() {
				x.push(Event{Kind: EventError, Err: ErrNoStructuredResponse})
			}
			if err := s.record(x); err != nil {
				x.push(Event{Kind: EventError, Err: err})
			}
			s.mu.Lock()
			if s.open == x {
				s.open = nil
			}
			s.runEnded = false
			s.mu.Unlock()
		}
		if x != nil {
			x.push(ev)
		}
	}

	exitErr := s.connection.Err()
	if exitErr == nil {
		exitErr = ErrClosed
	}
	s.mu.Lock()
	s.exited = true
	s.exitErr = exitErr
	x := s.open
	s.open = nil
	s.mu.Unlock()
	if x != nil {
		if err := s.put(x.record(nil, exitErr)); err != nil {
			x.push(Event{Kind: EventError, Err: err})
		}
		x.fail(exitErr)
	}
	close(s.eventsClosed)
}

func (s *Session) record(x *Exchange) error {
	if s.store == nil {
		return nil
	}
	entries, bindErr := s.bind(x)
	if err := s.put(x.record(entries, bindErr)); err != nil {
		return errors.Join(bindErr, err)
	}
	return bindErr
}

func (s *Session) bind(x *Exchange) ([]string, error) {
	if s.journal == nil {
		return nil, nil
	}
	s.mu.Lock()
	cursor := s.cursor
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, recordTimeout)
	defer cancel()
	ids, err := s.journal.Since(ctx, cursor)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.resync = true
		return nil, fmt.Errorf("harness: bind exchange %s to the journal: %w", x.ID(), err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	s.cursor = ids[len(ids)-1]
	return ids, nil
}

func (s *Session) put(rec Record) error {
	if s.store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	if err := s.store.Put(ctx, rec); err != nil {
		return fmt.Errorf("harness: store exchange %s: %w", rec.ExchangeID, err)
	}
	return nil
}
