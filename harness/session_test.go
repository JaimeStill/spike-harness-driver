package harness_test

import (
	"context"
	"encoding/json"
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
	// promptGate, when set, holds Prompt's answer until it closes or Prompt's context ends.
	promptGate chan struct{}
	// cancelGate, when set, holds Cancel's answer the same way; one that never closes is a
	// harness that never answers.
	cancelGate chan struct{}
	cancels    chan struct{}
	// exitErr is what Err reports: why the harness exited.
	exitErr   error
	closeOnce sync.Once
}

func newFakeConnection() *fakeConnection {
	return &fakeConnection{events: make(chan harness.Event), cancels: make(chan struct{}, 8)}
}

func (c *fakeConnection) Prompt(ctx context.Context, _ harness.Request) error {
	if err := gate(ctx, c.promptGate); err != nil {
		return err
	}
	return c.promptErr
}

func (c *fakeConnection) Cancel(ctx context.Context) error {
	c.cancels <- struct{}{}
	return gate(ctx, c.cancelGate)
}

// gate waits for g to close, or for ctx to end. A nil g doesn't wait.
func gate(ctx context.Context, g chan struct{}) error {
	if g == nil {
		return nil
	}
	select {
	case <-g:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// never checks that ch receives nothing for a while, for a thing that must not happen and
// would happen on another goroutine if it did.
func never[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal(what)
	case <-time.After(100 * time.Millisecond):
	}
}

func (c *fakeConnection) Events() <-chan harness.Event { return c.events }
func (c *fakeConnection) Err() error                   { return c.exitErr }

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

// newSession starts a session over c, recording in store, which may be nil.
func newSession(t *testing.T, c harness.Connection, store harness.Store) *harness.Session {
	t.Helper()
	s, err := harness.NewSession(t.Context(), "s1", c, store)
	if err != nil {
		t.Fatal(err)
	}
	return s
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
	s := newSession(t, c, nil)
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
	s := newSession(t, c, nil)
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
			s := newSession(t, c, nil)
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
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()

	x := send(t, s, t.Context())
	events := collect(t, x)
	c.finish("stop", "")
	<-events
	send(t, s, t.Context())
	// x has ended and another exchange is open; cancelling x must not reach the Connection, or
	// it would stop the open exchange.
	x.Cancel()
	never(t, c.cancels, "cancelling an ended exchange cancelled the Connection")
}

func TestConnExitEndsTheExchange(t *testing.T) {
	c := newFakeConnection()
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()

	x := send(t, s, t.Context())
	events := collect(t, x)
	boom := errors.New("harness exited: boom")
	c.emit(harness.EventStarted)
	c.exitErr = boom
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
	if _, err := x.Wait(); !errors.Is(err, boom) {
		t.Fatalf("Wait error = %v, want the exit error", err)
	}
	if _, err := s.Send(t.Context(), harness.Request{}); !errors.Is(err, boom) {
		t.Fatalf("Send after exit = %v, want the exit error", err)
	}
}

// A run that ends in an error is the run's error; the harness's exit later is the
// Connection's to report, not the last error event's.
func TestARunsErrorIsNotTheExitError(t *testing.T) {
	c := newFakeConnection()
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()

	failed := errors.New("model unreachable")
	x := send(t, s, t.Context())
	events := collect(t, x)
	c.emit(harness.EventStarted)
	c.events <- harness.Event{Kind: harness.EventError, Err: failed}
	c.emit(harness.EventEnded)
	<-events
	if _, err := x.Wait(); !errors.Is(err, failed) {
		t.Fatalf("Wait error = %v, want the run's error", err)
	}

	// The harness exits during the next exchange, which ends in the exit error alone.
	exited := errors.New("harness exited")
	x = send(t, s, t.Context())
	c.exitErr = exited
	_ = c.Close()
	drain(t, x)
	if _, err := x.Wait(); !errors.Is(err, exited) || errors.Is(err, failed) {
		t.Fatalf("Wait error = %v, want the exit error", err)
	}
	if _, err := s.Send(t.Context(), harness.Request{}); !errors.Is(err, exited) {
		t.Fatalf("Send after exit = %v, want the exit error", err)
	}
}

// structured runs one exchange with schema, emitting the structured response when there is
// one, and ending the run with stop. It returns the exchange and its events.
func structured(t *testing.T, c *fakeConnection, s *harness.Session, schema, response, stop string) (*harness.Exchange, []harness.Event) {
	t.Helper()
	x, err := s.Send(t.Context(), harness.Request{Text: "hi", Schema: json.RawMessage(schema)})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, x)
	c.emit(harness.EventStarted)
	if response != "" {
		c.events <- harness.Event{Kind: harness.EventStructured, Structured: json.RawMessage(response)}
	}
	c.finish(stop, "")
	return x, <-events
}

func TestAStructuredResponseFoldsIntoTheResult(t *testing.T) {
	c := newFakeConnection()
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()

	x, _ := structured(t, c, s, `{"type":"object"}`, `{"answer":42}`, "stop")
	res, err := x.Wait()
	if err != nil || string(res.Structured) != `{"answer":42}` {
		t.Fatalf("Wait = %s, %v", res.Structured, err)
	}
}

func TestAnUnansweredSchemaIsTheExchangesError(t *testing.T) {
	c := newFakeConnection()
	store := newMemStore()
	s := newSession(t, c, store)
	defer func() { _ = s.Close() }()

	x, all := structured(t, c, s, `{"type":"object"}`, "", "stop")
	if _, err := x.Wait(); !errors.Is(err, harness.ErrNoStructuredResponse) {
		t.Fatalf("Wait error = %v, want ErrNoStructuredResponse", err)
	}
	checkScoped(t, s, x, all)
	recs, _ := s.Exchanges(t.Context())
	if len(recs) != 1 || recs[0].Err != harness.ErrNoStructuredResponse.Error() {
		t.Fatalf("records = %+v", recs)
	}

	// A cancelled exchange owes no structured response, and neither does one without a schema.
	for _, tc := range []struct{ name, schema, stop string }{
		{"cancelled", `{"type":"object"}`, "aborted"},
		{"no schema", "", "stop"},
	} {
		x, _ := structured(t, c, s, tc.schema, "", tc.stop)
		if _, err := x.Wait(); err != nil {
			t.Fatalf("%s: Wait error = %v", tc.name, err)
		}
	}
}

func TestPromptFailure(t *testing.T) {
	c := newFakeConnection()
	c.promptErr = errors.New("rejected")
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()

	if _, err := s.Send(t.Context(), harness.Request{}); err == nil {
		t.Fatal("Send succeeded although the prompt was rejected")
	}
	c.promptErr = nil
	send(t, s, t.Context()) // a rejected prompt leaves no exchange open
}

func TestClose(t *testing.T) {
	c := newFakeConnection()
	s := newSession(t, c, nil)

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

func TestCloseStopsACancellationInFlight(t *testing.T) {
	c := newFakeConnection()
	c.cancelGate = make(chan struct{}) // never answers
	s := newSession(t, c, nil)

	x := send(t, s, t.Context())
	x.Cancel()
	<-c.cancels
	begin := time.Now()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("Close waited %s on the cancellation", d)
	}
	// The exchange ends because the session closed, not because its cancellation was cut
	// short.
	if _, err := x.Wait(); !errors.Is(err, harness.ErrClosed) {
		t.Fatalf("Wait error = %v, want ErrClosed", err)
	}
}

// TestContextEndsBeforePromptIsAccepted covers Send's ctx ending while the harness has the
// prompt but hasn't answered. Send still waits for the answer, then cancels the run it started,
// so no run is left without an exchange.
func TestContextEndsBeforePromptIsAccepted(t *testing.T) {
	c := newFakeConnection()
	c.promptGate = make(chan struct{})
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()

	ctx, stop := context.WithCancel(t.Context())
	sent := make(chan *harness.Exchange, 1)
	go func() {
		x, err := s.Send(ctx, harness.Request{})
		if err != nil {
			t.Error(err)
		}
		sent <- x
	}()
	stop()
	never(t, c.cancels, "the run was cancelled before the harness accepted it")
	close(c.promptGate)
	x := <-sent
	select {
	case <-c.cancels:
	case <-time.After(5 * time.Second):
		t.Fatal("the accepted run was not cancelled")
	}
	events := collect(t, x)
	c.finish("aborted", "")
	checkScoped(t, s, x, <-events)
}

// TestSendWaitsForACancellationInFlight covers an exchange that ends while its cancellation is
// still in flight. The next Send waits for the cancellation, which would otherwise reach the
// next exchange's run.
func TestSendWaitsForACancellationInFlight(t *testing.T) {
	c := newFakeConnection()
	c.cancelGate = make(chan struct{})
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()

	x := send(t, s, t.Context())
	events := collect(t, x)
	x.Cancel()
	<-c.cancels
	c.finish("aborted", "") // x ends before the harness answers the cancellation
	<-events

	short, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()
	if _, err := s.Send(short, harness.Request{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send during the cancellation = %v, want it to wait until its ctx ends", err)
	}

	sent := make(chan error, 1)
	go func() {
		_, err := s.Send(t.Context(), harness.Request{})
		sent <- err
	}()
	never(t, sent, "Send did not wait for the cancellation")
	close(c.cancelGate)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}

// A cancelled exchange owes no structured response even when the harness ends the run without
// reporting the cancellation, as when the cancel lands during a tool call.
func TestACancelWithoutACancelledEventOwesNoResponse(t *testing.T) {
	c := newFakeConnection()
	s := newSession(t, c, nil)
	defer func() { _ = s.Close() }()

	x, err := s.Send(t.Context(), harness.Request{Text: "hi", Schema: json.RawMessage(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, x)
	c.emit(harness.EventStarted)
	x.Cancel()
	<-c.cancels
	c.finish("toolUse", "")
	<-events
	if _, err := x.Wait(); err != nil {
		t.Fatalf("Wait error = %v, want none for a cancelled exchange", err)
	}
}

func TestSendRefusesAnInvalidSchema(t *testing.T) {
	s := newSession(t, newFakeConnection(), nil)
	defer func() { _ = s.Close() }()
	for _, schema := range []string{`{`, `[]`, `{}`, `{"type":"array"}`} {
		if _, err := s.Send(t.Context(), harness.Request{Text: "hi", Schema: json.RawMessage(schema)}); !errors.Is(err, harness.ErrInvalidSchema) {
			t.Errorf("schema %s: Send = %v, want ErrInvalidSchema", schema, err)
		}
	}
	// A refused request opens no exchange.
	send(t, s, t.Context())
}

func TestToolValidate(t *testing.T) {
	handler := func(context.Context, json.RawMessage) (string, error) { return "", nil }
	for _, tc := range []struct {
		tool harness.Tool
		ok   bool
	}{
		{harness.Tool{Name: "lookup_code", Handler: handler}, true},
		{harness.Tool{Name: "a-b", Handler: handler, Schema: json.RawMessage(`{"type":"object"}`)}, true},
		{harness.Tool{Name: "", Handler: handler}, false},
		{harness.Tool{Name: "a,b", Handler: handler}, false},
		{harness.Tool{Name: "ok"}, false},
		{harness.Tool{Name: "ok", Handler: handler, Schema: json.RawMessage(`{"type":"string"}`)}, false},
	} {
		if err := tc.tool.Validate(); (err == nil) != tc.ok {
			t.Errorf("%+v: Validate = %v, want ok %v", tc.tool.Name, err, tc.ok)
		}
	}
}
