package harness_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// fakeConnection is a Connection the test scripts: it records prompts and cancellations, and the
// test emits the harness's events by hand.
type fakeConnection struct {
	events    chan harness.Event
	promptErr error
	cancels   chan struct{}
	closeOnce sync.Once
}

func newFakeConnection() *fakeConnection {
	return &fakeConnection{events: make(chan harness.Event), cancels: make(chan struct{}, 8)}
}

func (c *fakeConnection) Prompt(context.Context, harness.Request) error { return c.promptErr }

func (c *fakeConnection) Cancel(context.Context) error {
	c.cancels <- struct{}{}
	return nil
}

func (c *fakeConnection) Events() <-chan harness.Event { return c.events }

func (c *fakeConnection) Close() error {
	c.closeOnce.Do(func() { close(c.events) })
	return nil
}

func (c *fakeConnection) emit(kinds ...harness.EventKind) {
	for _, k := range kinds {
		c.events <- harness.Event{Kind: k}
	}
}

func (c *fakeConnection) finish(stop, text string) {
	c.events <- harness.Event{Kind: harness.EventMessageEnd, StopReason: stop, Text: text,
		Usage: &harness.Usage{Input: 3, Output: 2}}
	if stop == "aborted" {
		c.events <- harness.Event{Kind: harness.EventCancelled, StopReason: stop}
	}
	c.events <- harness.Event{Kind: harness.EventEnded}
}

// drain collects an exchange's events until its channel closes.
func drain(t *testing.T, x *harness.Exchange) []harness.Event {
	t.Helper()
	var events []harness.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-x.Events():
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatalf("exchange %s did not end; got %d events", x.ID(), len(events))
		}
	}
}

// collect drains x in the background, so the test can emit events without blocking on it.
func collect(t *testing.T, x *harness.Exchange) <-chan []harness.Event {
	out := make(chan []harness.Event, 1)
	go func() { out <- drain(t, x) }()
	return out
}

func checkScoped(t *testing.T, s *harness.Session, x *harness.Exchange, events []harness.Event) {
	t.Helper()
	for i, ev := range events {
		if ev.SessionID != s.ID() || ev.ExchangeID != x.ID() || ev.Seq != i+1 {
			t.Fatalf("event %d = {%s %s %d}, want {%s %s %d}",
				i, ev.SessionID, ev.ExchangeID, ev.Seq, s.ID(), x.ID(), i+1)
		}
	}
	if len(events) == 0 || events[len(events)-1].Kind != harness.EventEnded {
		t.Fatalf("exchange did not end with EventEnded: %v", events)
	}
}

func send(t *testing.T, s *harness.Session, ctx context.Context) *harness.Exchange {
	t.Helper()
	x, err := s.Send(ctx, harness.Request{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestTwoExchangesInOneSession(t *testing.T) {
	c := newFakeConnection()
	s := harness.NewSession("s1", c)
	defer func() { _ = s.Close() }()

	var xs []*harness.Exchange
	for _, text := range []string{"one", "two"} {
		x := send(t, s, t.Context())
		events := collect(t, x)
		c.emit(harness.EventStarted, harness.EventTextDelta)
		c.finish("stop", text)
		all := <-events
		checkScoped(t, s, x, all)
		if end := all[len(all)-1]; end.StopReason != "stop" {
			t.Errorf("EventEnded StopReason = %q, want stop", end.StopReason)
		}
		res, err := x.Wait()
		if err != nil || res.StopReason != "stop" || res.Text != text || res.Usage.Output != 2 {
			t.Fatalf("Wait = %+v, %v", res, err)
		}
		xs = append(xs, x)
	}
	if xs[0].ID().Compare(xs[1].ID()) >= 0 {
		t.Fatalf("exchange IDs %s and %s are not increasing", xs[0].ID(), xs[1].ID())
	}
}

func TestSendWhileOpenIsBusy(t *testing.T) {
	c := newFakeConnection()
	s := harness.NewSession("s1", c)
	defer func() { _ = s.Close() }()

	x := send(t, s, t.Context())
	if _, err := s.Send(t.Context(), harness.Request{}); !errors.Is(err, harness.ErrBusy) {
		t.Fatalf("second Send = %v, want ErrBusy", err)
	}
	events := collect(t, x)
	c.finish("stop", "")
	<-events
	send(t, s, t.Context()) // the session accepts a prompt once the exchange ends
}

func TestCancel(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(x *harness.Exchange, stop context.CancelFunc)
	}{
		{name: "Cancel", cancel: func(x *harness.Exchange, _ context.CancelFunc) { x.Cancel() }},
		{name: "context", cancel: func(_ *harness.Exchange, stop context.CancelFunc) { stop() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newFakeConnection()
			s := harness.NewSession("s1", c)
			defer func() { _ = s.Close() }()
			ctx, stop := context.WithCancel(t.Context())
			defer stop()

			x := send(t, s, ctx)
			events := collect(t, x)
			c.emit(harness.EventStarted, harness.EventTextDelta)
			tt.cancel(x, stop)
			select {
			case <-c.cancels:
			case <-time.After(5 * time.Second):
				t.Fatal("the Connection was not cancelled")
			}
			c.finish("aborted", "partial")
			checkScoped(t, s, x, <-events)
			res, err := x.Wait()
			if err != nil || res.StopReason != "aborted" {
				t.Fatalf("Wait = %+v, %v; want aborted, nil", res, err)
			}
		})
	}
}

func TestCancelAfterEndDoesNothing(t *testing.T) {
	c := newFakeConnection()
	s := harness.NewSession("s1", c)
	defer func() { _ = s.Close() }()

	x := send(t, s, t.Context())
	events := collect(t, x)
	c.finish("stop", "")
	<-events
	send(t, s, t.Context())
	// x has ended and another exchange is open; cancelling x must not reach the Connection, or it
	// would stop the open exchange. The check is synchronous, so no wait is needed.
	x.Cancel()
	if len(c.cancels) != 0 {
		t.Fatal("cancelling an ended exchange cancelled the Connection")
	}
}

func TestConnExitEndsTheExchange(t *testing.T) {
	c := newFakeConnection()
	s := harness.NewSession("s1", c)
	defer func() { _ = s.Close() }()

	x := send(t, s, t.Context())
	events := collect(t, x)
	c.emit(harness.EventStarted)
	c.events <- harness.Event{Kind: harness.EventError, Err: "harness exited: boom"}
	_ = c.Close()

	all := <-events
	checkScoped(t, s, x, all)
	errs := 0
	for _, ev := range all {
		if ev.Kind == harness.EventError {
			errs++
		}
	}
	if errs != 1 {
		t.Errorf("got %d error events, want 1", errs)
	}
	if _, err := x.Wait(); err == nil || err.Error() != "harness exited: boom" {
		t.Fatalf("Wait error = %v, want the exit error", err)
	}
	if _, err := s.Send(t.Context(), harness.Request{}); err == nil || err.Error() != "harness exited: boom" {
		t.Fatalf("Send after exit = %v, want the exit error", err)
	}
}

func TestPromptFailure(t *testing.T) {
	c := newFakeConnection()
	c.promptErr = errors.New("rejected")
	s := harness.NewSession("s1", c)
	defer func() { _ = s.Close() }()

	if _, err := s.Send(t.Context(), harness.Request{}); err == nil {
		t.Fatal("Send succeeded although the prompt was rejected")
	}
	c.promptErr = nil
	send(t, s, t.Context()) // a rejected prompt leaves no exchange open
}

func TestClose(t *testing.T) {
	c := newFakeConnection()
	s := harness.NewSession("s1", c)

	x := send(t, s, t.Context())
	c.emit(harness.EventStarted, harness.EventTextDelta, harness.EventTextDelta)
	// Nothing has read x's events; Close must still release them.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	drain(t, x)
	if _, err := s.Send(t.Context(), harness.Request{}); !errors.Is(err, harness.ErrClosed) {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}
}
