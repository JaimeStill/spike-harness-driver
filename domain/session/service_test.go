package session_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/output"
)

// longPrompt makes the stub stream text deltas until it is cancelled.
const longPrompt = "long"

// stubDriver opens sessions over stubConnection.
type stubDriver struct {
	opened *harness.Options
}

func (d stubDriver) Open(_ context.Context, opts harness.Options) (*harness.Session, error) {
	if d.opened != nil {
		*d.opened = opts
	}
	c := &stubConnection{events: make(chan harness.Event, 64), cancel: make(chan struct{})}
	return harness.NewSession("stub-session", c), nil
}

// stubConnection answers a prompt with a short run, and the long prompt with deltas until
// Cancel, which ends the run as aborted.
type stubConnection struct {
	events    chan harness.Event
	cancel    chan struct{}
	closeOnce sync.Once
}

func (c *stubConnection) Prompt(_ context.Context, req harness.Request) error {
	go func() {
		c.events <- harness.Event{Kind: harness.EventStarted}
		if req.Text != longPrompt {
			c.events <- harness.Event{Kind: harness.EventTextDelta, Text: "Hi"}
			c.events <- harness.Event{Kind: harness.EventMessageEnd, Text: "Hi", StopReason: "stop"}
			c.events <- harness.Event{Kind: harness.EventEnded}
			return
		}
		for {
			select {
			case <-c.cancel:
				c.events <- harness.Event{Kind: harness.EventMessageEnd, StopReason: "aborted"}
				c.events <- harness.Event{Kind: harness.EventCancelled, StopReason: "aborted"}
				c.events <- harness.Event{Kind: harness.EventEnded}
				return
			case c.events <- harness.Event{Kind: harness.EventTextDelta, Text: "."}:
			}
		}
	}()
	return nil
}

func (c *stubConnection) Cancel(context.Context) error {
	close(c.cancel)
	return nil
}

func (c *stubConnection) Events() <-chan harness.Event { return c.events }

func (c *stubConnection) Close() error {
	c.closeOnce.Do(func() { close(c.events) })
	return nil
}

func newService(opened *harness.Options) *session.Service {
	return session.New(
		func() (harness.Driver, error) { return stubDriver{opened: opened}, nil },
		func() harness.Options { return harness.Options{Provider: "p", Model: "m"} },
	)
}

func TestRunToTheEnd(t *testing.T) {
	var opened harness.Options
	svc := newService(&opened)
	sess, err := svc.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if opened.Provider != "p" || opened.Model != "m" {
		t.Errorf("opened with %+v", opened)
	}

	var kinds []harness.EventKind
	sent := 0
	o, err := svc.Run(t.Context(), sess, session.Exchange{Prompt: "hi"}, session.Observer{
		Sent:  func(uuid.UUID) { sent++ },
		Event: func(ev harness.Event) { kinds = append(kinds, ev.Kind) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Errorf("Sent called %d times", sent)
	}
	if o.Result.StopReason != "stop" || o.Result.Text != "Hi" || o.Err != nil {
		t.Errorf("outcome = %+v", o)
	}
	if len(kinds) != 4 || kinds[len(kinds)-1] != harness.EventEnded {
		t.Errorf("events = %v", kinds)
	}
}

func TestRunCancelsAfterDeltas(t *testing.T) {
	svc := newService(nil)
	sess, err := svc.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	cancelledAt := 0
	o, err := svc.Run(t.Context(), sess, session.Exchange{Prompt: longPrompt, CancelAfter: 3}, session.Observer{
		Cancelling: func(n int) { cancelledAt = n },
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelledAt != 3 {
		t.Errorf("cancelled after %d deltas, want 3", cancelledAt)
	}
	if o.Result.StopReason != "aborted" || o.Err != nil {
		t.Errorf("outcome = %+v", o)
	}
}

func TestOpenFailsWithTheDriverError(t *testing.T) {
	boom := errors.New("no such harness")
	svc := session.New(func() (harness.Driver, error) { return nil, boom }, func() harness.Options { return harness.Options{} })
	if _, err := svc.Open(t.Context()); !errors.Is(err, boom) {
		t.Errorf("Open = %v, want %v", err, boom)
	}
}

func TestSendCommand(t *testing.T) {
	var out, errs bytes.Buffer
	cmd := session.Commands(newService(nil), output.New(&out, &errs, nil))
	cmd.SetArgs([]string{"send", "hi"})
	cmd.SetOut(&out)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"session stub-session\n", `: "hi"` + "\n", "ended", "result: stop=stop"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}
