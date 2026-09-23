package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// session is one Pi process. It carries one exchange at a time.
type session struct {
	id           string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stderr       bytes.Buffer // written by cmd, read only after cmd.Wait
	closeTimeout time.Duration

	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[string]chan record
	active  *exchange
	exited  bool
	exitErr error
	closing bool

	done   chan struct{} // closed when Pi has exited and read has returned
	closed chan struct{} // closed by Close; releases undrained exchanges
}

func (s *session) ID() string { return s.id }

// Send opens an exchange and prompts Pi with req. It returns ErrBusy while another exchange is
// open: Pi rejects a prompt during a run unless it is queued, and a queued prompt's events
// could not be told apart from the running one's.
func (s *session) Send(ctx context.Context, req harness.Request) (harness.Exchange, error) {
	s.mu.Lock()
	switch {
	case s.closing:
		s.mu.Unlock()
		return nil, harness.ErrClosed
	case s.exited:
		s.mu.Unlock()
		return nil, s.exitErr
	case s.active != nil:
		s.mu.Unlock()
		return nil, harness.ErrBusy
	}
	x := newExchange(s)
	s.active = x
	s.mu.Unlock()
	go x.pump()

	// The response only means Pi accepted the prompt; the run's outcome arrives as events.
	if _, err := s.call(ctx, command{Type: "prompt", Message: req.Text}); err != nil {
		s.mu.Lock()
		if s.active == x {
			s.active = nil
		}
		s.mu.Unlock()
		x.push(harness.Event{Kind: harness.KindError, Err: err.Error()})
		x.push(harness.Event{Kind: harness.KindEnded})
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

// Close closes Pi's stdin, which shuts Pi down, and kills Pi if it hasn't exited within the
// close timeout. Events an exchange hasn't delivered yet are discarded.
func (s *session) Close() error {
	s.mu.Lock()
	first := !s.closing
	s.closing = true
	s.mu.Unlock()
	if first {
		_ = s.stdin.Close()
		select {
		case <-s.done:
		case <-time.After(s.closeTimeout):
			_ = s.cmd.Process.Kill()
			<-s.done
		}
		close(s.closed)
	}
	<-s.closed
	if s.cmd.ProcessState.Success() {
		return nil
	}
	return s.exitErr
}

// call writes one command and waits for its response, correlated by id.
func (s *session) call(ctx context.Context, c command) (record, error) {
	c.ID = "c" + strconv.FormatInt(s.nextID.Add(1), 10)
	ch := make(chan record, 1)
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return record{}, s.exitErr
	}
	s.pending[c.ID] = ch
	s.mu.Unlock()

	line, err := json.Marshal(c)
	if err != nil {
		return record{}, fmt.Errorf("pi: encode %s: %w", c.Type, err)
	}
	s.writeMu.Lock()
	_, err = s.stdin.Write(append(line, '\n'))
	s.writeMu.Unlock()
	if err != nil {
		s.forget(c.ID)
		return record{}, fmt.Errorf("pi: write %s: %w", c.Type, err)
	}

	select {
	case r, ok := <-ch:
		if !ok {
			return record{}, s.exitErr
		}
		if !r.Success {
			return r, fmt.Errorf("pi: %s: %s", c.Type, r.Error)
		}
		return r, nil
	case <-ctx.Done():
		s.forget(c.ID)
		return record{}, ctx.Err()
	}
}

func (s *session) forget(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *session) isActive(x *exchange) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active == x
}

// abortTimeout bounds the clear_queue and abort calls a cancellation makes.
const abortTimeout = 30 * time.Second

// exchange is one prompt's run, from agent_start to agent_settled. Events queue without bound,
// so the stdout reader never waits on a slow consumer, and pump delivers them in order.
type exchange struct {
	id string
	s  *session

	mu     sync.Mutex
	queue  []harness.Event
	seq    int
	final  bool
	result harness.Result
	err    error

	wake       chan struct{}
	events     chan harness.Event
	ended      chan struct{}
	cancelOnce sync.Once
}

func newExchange(s *session) *exchange {
	return &exchange{
		id:     harness.NewID(),
		s:      s,
		wake:   make(chan struct{}, 1),
		events: make(chan harness.Event),
		ended:  make(chan struct{}),
	}
}

func (x *exchange) ID() string                   { return x.id }
func (x *exchange) Events() <-chan harness.Event { return x.events }

// Cancel clears Pi's queue, so no queued message starts another run, and aborts the run. Pi
// then ends the assistant message with stopReason "aborted", which normalizes to
// KindCancelled, and settles.
func (x *exchange) Cancel() {
	x.cancelOnce.Do(func() {
		go func() {
			// The run may have settled already; an abort then would hit the next exchange.
			if !x.s.isActive(x) {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), abortTimeout)
			defer cancel()
			_, err := x.s.call(ctx, command{Type: "clear_queue"})
			if err == nil {
				_, err = x.s.call(ctx, command{Type: "abort"})
			}
			if err != nil {
				x.push(harness.Event{Kind: harness.KindError, Err: err.Error()})
			}
		}()
	})
}

func (x *exchange) Wait() (harness.Result, error) {
	<-x.ended
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.result, x.err
}

// push stamps ev with the exchange's IDs and next sequence number, folds it into the result,
// and queues it. Events after KindEnded are dropped.
func (x *exchange) push(ev harness.Event) {
	x.mu.Lock()
	if x.final {
		x.mu.Unlock()
		return
	}
	x.seq++
	ev.SessionID, ev.ExchangeID, ev.Seq = x.s.id, x.id, x.seq
	switch ev.Kind {
	case harness.KindMessageEnd:
		x.result.StopReason, x.result.Text = ev.StopReason, ev.Text
		if ev.Usage != nil {
			x.result.Usage = *ev.Usage
		}
	case harness.KindError:
		if x.err == nil {
			x.err = errors.New(ev.Err)
		}
	case harness.KindEnded:
		ev.StopReason = x.result.StopReason
		x.final = true
	}
	x.queue = append(x.queue, ev)
	x.mu.Unlock()

	if ev.Kind == harness.KindEnded {
		close(x.ended)
	}
	select {
	case x.wake <- struct{}{}:
	default:
	}
}

// pump delivers queued events until it delivers KindEnded or the session closes.
func (x *exchange) pump() {
	defer close(x.events)
	for {
		x.mu.Lock()
		batch := x.queue
		x.queue = nil
		x.mu.Unlock()
		for _, ev := range batch {
			select {
			case x.events <- ev:
			case <-x.s.closed:
				return
			}
			if ev.Kind == harness.KindEnded {
				return
			}
		}
		if len(batch) == 0 {
			select {
			case <-x.wake:
			case <-x.s.closed:
				return
			}
		}
	}
}
