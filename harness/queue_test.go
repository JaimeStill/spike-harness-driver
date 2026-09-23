package harness_test

import (
	"context"
	"testing"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// read collects a queue's events until Events closes.
func read(t *testing.T, q *harness.EventQueue) []harness.Event {
	t.Helper()
	var events []harness.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-q.Events():
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatalf("Events did not close; got %d events", len(events))
		}
	}
}

func TestEventQueueKeepsOrderWithoutAReader(t *testing.T) {
	q := harness.NewEventQueue(t.Context())
	for i := range 1000 {
		q.Push(harness.Event{Seq: i})
	}
	q.Close()
	q.Push(harness.Event{Seq: -1}) // dropped: the queue is closed
	events := read(t, q)
	if len(events) != 1000 {
		t.Fatalf("got %d events, want 1000", len(events))
	}
	for i, ev := range events {
		if ev.Seq != i {
			t.Fatalf("event %d has Seq %d", i, ev.Seq)
		}
	}
}

func TestEventQueueEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	q := harness.NewEventQueue(ctx)
	q.Push(harness.Event{}, harness.Event{})
	cancel()
	// Nothing read the events; ending the queue's context closes Events anyway, and may
	// discard them.
	if n := len(read(t, q)); n > 2 {
		t.Fatalf("got %d events after release", n)
	}
}
